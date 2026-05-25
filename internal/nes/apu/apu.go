// Package apu models the NES audio processing unit (2A03's APU half).
//
// v0.2 ships the two pulse-wave channels ($4000-$4007), the frame
// counter at $4017, and a 44.1 kHz mono int16 sample ring. Triangle,
// noise, and DMC channels are v0.3 work.
//
// CPU bus layout:
//   - $4000-$4003  pulse 1 registers
//   - $4004-$4007  pulse 2 registers
//   - $4008-$400B  triangle (v0.3 stub: writes accepted, output silent)
//   - $400C-$400F  noise    (v0.3 stub: writes accepted, output silent)
//   - $4010-$4013  DMC      (v0.3 stub: writes accepted, output silent)
//   - $4015        status / channel enables
//   - $4017        frame counter mode + IRQ inhibit
//     (this address sits in joypad.Port's range; the
//     joypad forwards writes to APU.SetFrameCounter
//     during wiring — see internal/nes/joypad.Port and
//     cmd/nessy/wiring.go)
//
// Bus-ticker integration: APU implements cpu.Ticker. cpu.Step()
// invokes Tick(cpuCycles) at every instruction boundary; the APU
// advances its frame counter at CPU rate and its pulse timers at
// half CPU rate (real silicon clocks the pulse units every other
// CPU cycle).
package apu

import (
	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/nes"
)

// Sample rate the APU's int16 ring buffer emits at. 44.1 kHz is the
// standard CD-quality target and the most common Ebiten audio
// context rate.
const SampleRate = 44100

// NTSC reference clock + frame-counter step, used as the defaults +
// the values the existing APU tests pin against. Runtime reads
// a.cpuClockHz / a.quarterFrameCycles, which default to these via
// nes.NTSC and switch under SetRegion for PAL / Dendy carts.
const (
	cpuClockHz         = 1789773 // NTSC CPU clock (Hz)
	quarterFrameCycles = 7457    // NTSC 240 Hz frame-counter step (CPU cycles)
)

// IRQSink is the CPU's named-source IRQ surface from the APU's
// point of view (see cpu.AssertIRQSource / cpu.ClearIRQSource). The
// APU asserts under name "apu-frame" at the end of each 4-step
// frame counter cycle (unless inhibited) and clears the source on
// $4015 read or $4017 inhibit write. DMC IRQ wiring lands with the
// DMC channel itself (issue #246).
type IRQSink interface {
	AssertIRQSource(src string)
	ClearIRQSource(src string)
}

// frameIRQSource is the name the APU uses on cpu.AssertIRQSource for
// its frame-counter IRQ. Exported as a constant so callers wiring
// the sink can see the contract without grepping.
const frameIRQSource = "apu-frame"

// APU is the 2A03 audio half. Claims $4000-$4015 on the CPU bus;
// the $4017 frame-counter write comes in from the joypad's $4017
// forwarder so the two peripherals don't fight over the same
// register.
type APU struct {
	pulse1   pulseChannel
	pulse2   pulseChannel
	triangle triangleChannel
	noise    noiseChannel
	dmc      dmcChannel

	// DMC needs CPU-bus access (sample fetch) + the stall hook.
	// Late-bound via SetDMCBus so the APU can be constructed
	// before the CPU exists.
	dmcBus     DMCBus
	dmcStaller DMCStaller

	// Frame counter state. mode4Step true = 4-step (the default,
	// 240 Hz IRQ ticks); false = 5-step (no IRQ). irqInhibit gates
	// the 4-step IRQ.
	mode4Step    bool
	irqInhibit   bool
	frameStep    int // 0-3 in 4-step, 0-4 in 5-step
	frameTimer   int // CPU cycles until the next step boundary
	frameIRQFlag bool

	// sunsoft5b (optional) is the audio half of the FME-7 mapper
	// package. nil unless cmd/nessy wires it via SetSunsoft5B
	// during cart construction. Output is folded into emitSample's
	// mix.
	sunsoft5b *Sunsoft5B

	// vrc6Audio (optional) is the 3-channel expansion on Konami's
	// VRC6 cart (mappers 24/26). Same pattern as sunsoft5b.
	vrc6Audio *VRC6Audio

	// vrc7Audio (optional) is the YM2413 / OPLL FM-synth chip on
	// Konami's VRC7 cart (mapper 85) — 6-channel 2-op FM (#315).
	vrc7Audio *VRC7Audio

	// irqSink (optional) is the CPU's IRQ line. nil means
	// "headless" — registers still track the IRQ flag but nothing
	// is asserted on the CPU. cmd/nessy wires this via SetIRQSink
	// after both APU + CPU are constructed.
	irqSink IRQSink

	// Pulse units run at half CPU rate; alternateTick toggles each
	// CPU cycle and triggers the pulse-timer step on every other.
	alternateTick bool

	// Sample emission. sampleAccum keeps a fractional cycle-per-
	// sample accumulator so the int16 ring buffer stays locked at
	// SampleRate over long horizons.
	sampleAccum int
	samples     []int16
	samplesMax  int

	// Region-specific clock + frame-counter step. Default NTSC;
	// SetRegion swaps for PAL / Dendy carts.
	cpuClockHz         int
	quarterFrameCycles int
}

// New constructs an APU with the standard NTSC sample rate + a
// generously sized ring so a few frames of pending audio don't get
// dropped before the host drains them.
func New() *APU {
	pulse2 := pulseChannel{channelTwo: true}
	a := &APU{
		pulse2:    pulse2,
		noise:     noiseChannel{lfsr: 1},
		mode4Step: true,
		// First quarter-frame fires at the step mark, not at cycle 0.
		// Initialize the timer so stepCPU drains down to the boundary
		// correctly.
		frameTimer:         quarterFrameCycles,
		samplesMax:         SampleRate / 4, // ~250 ms of buffered audio
		cpuClockHz:         cpuClockHz,
		quarterFrameCycles: quarterFrameCycles,
	}
	a.samples = make([]int16, 0, a.samplesMax)
	return a
}

// SetRegion swaps the APU's clock + frame-counter step for PAL /
// Dendy carts. Re-seats the running frame timer onto the new step
// length. Call before stepping.
func (a *APU) SetRegion(t nes.Timing) {
	a.cpuClockHz = t.CPUClockHz
	a.quarterFrameCycles = t.QuarterFrameCycles
	a.frameTimer = t.QuarterFrameCycles
}

// Range claims $4000-$4013 — the channel-register window. $4014 is
// the OAMDMA peripheral's territory (registered separately in
// cmd/nessy); $4015 is the APU status register, exposed via the
// StatusPeripheral wrapper so MMIO sees a discontiguous "APU"
// surface without a brittle shared-range overlap. $4016 / $4017
// stay on joypad.Port; the $4017 frame-counter write flows in via
// SetFrameCounter.
func (a *APU) Range() (uint16, uint16) { return 0x4000, 0x4013 }

// StatusPeripheral wraps the APU's $4015 register so MMIO sees a
// dedicated single-byte peripheral that doesn't collide with the
// $4014 OAMDMA window. Always paired with the APU it shares state
// with; the wrapper is stateless and just forwards.
type StatusPeripheral struct{ apu *APU }

// NewStatus returns a $4015 wrapper bound to the given APU.
func NewStatus(a *APU) *StatusPeripheral { return &StatusPeripheral{apu: a} }

// Range claims exactly $4015.
func (s *StatusPeripheral) Range() (uint16, uint16) { return 0x4015, 0x4015 }

// Read forwards to APU.Read so the IRQ-clear side-effect on $4015
// read still fires correctly.
func (s *StatusPeripheral) Read(addr uint16) byte { return s.apu.Read(addr) }

// Write forwards to APU.Write so the per-channel enable / length-
// counter clear behavior on $4015 writes still fires.
func (s *StatusPeripheral) Write(addr uint16, v byte) { s.apu.Write(addr, v) }

// SetIRQSink wires the CPU's named-source IRQ surface. May be nil
// in tests / headless contexts. Frame-counter IRQ assertions
// before SetIRQSink lands harmlessly on the flag-only path.
func (a *APU) SetIRQSink(s IRQSink) { a.irqSink = s }

// SetSunsoft5B wires the audio half of the FME-7 cart so its
// channel mix gets folded into the APU's sample emission. nil
// disables the addend (the default for non-FME7 carts).
func (a *APU) SetSunsoft5B(s *Sunsoft5B) { a.sunsoft5b = s }

// Sunsoft5B returns the active 5B chip pointer (or nil). Used by
// the cart wiring to expose the chip for FME-7's port forwarding.
func (a *APU) Sunsoft5B() *Sunsoft5B { return a.sunsoft5b }

// SetVRC6Audio wires the VRC6 audio expansion (3-channel).
func (a *APU) SetVRC6Audio(v *VRC6Audio) { a.vrc6Audio = v }

// VRC6Audio returns the active chip pointer (or nil).
func (a *APU) VRC6Audio() *VRC6Audio { return a.vrc6Audio }

// SetVRC7Audio wires the VRC7 OPLL stub.
func (a *APU) SetVRC7Audio(v *VRC7Audio) { a.vrc7Audio = v }

// VRC7Audio returns the active chip pointer (or nil).
func (a *APU) VRC7Audio() *VRC7Audio { return a.vrc7Audio }

// Per-channel length-counter accessors. Headless test code uses
// these to assert "channel still active" without grabbing internal
// fields. Side-effect-free.
func (a *APU) Pulse1LengthCounter() byte   { return a.pulse1.lengthCounter }
func (a *APU) Pulse2LengthCounter() byte   { return a.pulse2.lengthCounter }
func (a *APU) TriangleLengthCounter() byte { return a.triangle.lengthCounter }
func (a *APU) NoiseLengthCounter() byte    { return a.noise.lengthCounter }
func (a *APU) DMCBytesRemaining() uint16   { return a.dmc.bytesRemaining }

// SetDMCBus wires the CPU bus the DMC reads sample bytes from and
// the cpu.Stall hook the DMA byte-fetch charges. Optional — when
// either argument is nil the DMC channel still tracks state but
// stops fetching new sample bytes (its current buffer drains
// silently and the channel goes mute).
func (a *APU) SetDMCBus(bus DMCBus, staller DMCStaller) {
	a.dmcBus = bus
	a.dmcStaller = staller
}

// Read services CPU reads. $4015 returns the per-channel status +
// IRQ flags; reading also clears the frame-IRQ flag (per nesdev).
// Other register addresses return open-bus 0.
func (a *APU) Read(addr uint16) byte {
	if addr != 0x4015 {
		return 0
	}
	var v byte
	if a.pulse1.lengthCounter > 0 {
		v |= 0x01
	}
	if a.pulse2.lengthCounter > 0 {
		v |= 0x02
	}
	if a.triangle.lengthCounter > 0 {
		v |= 0x04
	}
	if a.noise.lengthCounter > 0 {
		v |= 0x08
	}
	// DMC (bit 4) bit lands with #246. DMC IRQ at bit 7 also waits
	// on #246.
	if a.dmc.bytesRemaining > 0 {
		v |= 0x10
	}
	if a.frameIRQFlag {
		v |= 0x40
	}
	if a.dmc.irqPending {
		v |= 0x80
	}
	// Reading $4015 clears the frame-IRQ flag. DMC IRQ has its own
	// ack path via dmc.clearIRQ — also fired here per nesdev (one
	// $4015 read acks both).
	a.frameIRQFlag = false
	if a.irqSink != nil {
		a.irqSink.ClearIRQSource(frameIRQSource)
	}
	a.dmc.clearIRQ(a.irqSink)
	return v
}

// Write dispatches register writes to the relevant channel /
// shared unit. Triangle / noise / DMC writes are accepted but
// silently ignored until v0.3.
func (a *APU) Write(addr uint16, v byte) {
	switch addr {
	case 0x4000:
		a.pulse1.writeReg0(v)
	case 0x4001:
		a.pulse1.writeReg1(v)
	case 0x4002:
		a.pulse1.writeReg2(v)
	case 0x4003:
		a.pulse1.writeReg3(v)
	case 0x4004:
		a.pulse2.writeReg0(v)
	case 0x4005:
		a.pulse2.writeReg1(v)
	case 0x4006:
		a.pulse2.writeReg2(v)
	case 0x4007:
		a.pulse2.writeReg3(v)
	case 0x4008:
		a.triangle.writeReg0(v)
	case 0x400A:
		a.triangle.writeReg2(v)
	case 0x400B:
		a.triangle.writeReg3(v)
	case 0x400C:
		a.noise.writeReg0(v)
	case 0x400E:
		a.noise.writeReg2(v)
	case 0x400F:
		a.noise.writeReg3(v)
	case 0x4010:
		a.dmc.writeReg0(v)
		// Bit 7 clear in writeReg0 already clears DMC IRQ flag in
		// the channel; also drop the sink-side assertion so the
		// CPU line goes low.
		if v&0x80 == 0 && a.irqSink != nil {
			a.irqSink.ClearIRQSource(dmcIRQSource)
		}
	case 0x4011:
		a.dmc.writeReg1(v)
	case 0x4012:
		a.dmc.writeReg2(v)
	case 0x4013:
		a.dmc.writeReg3(v)
	case 0x4015:
		a.pulse1.setEnabled(v&0x01 != 0)
		a.pulse2.setEnabled(v&0x02 != 0)
		a.triangle.setEnabled(v&0x04 != 0)
		a.noise.setEnabled(v&0x08 != 0)
		a.dmc.setEnabled(v&0x10 != 0)
		// Writing $4015 also clears the DMC IRQ flag (per nesdev).
		a.dmc.clearIRQ(a.irqSink)
	}
}

// SetFrameCounter accepts the $4017 write forwarded from
// joypad.Port. Bit 7 = mode (0 = 4-step, 1 = 5-step); bit 6 = IRQ
// inhibit. Inhibit set also clears any pending frame IRQ
// immediately (per nesdev). 5-step mode never fires the IRQ.
func (a *APU) SetFrameCounter(v byte) {
	a.mode4Step = v&0x80 == 0
	a.irqInhibit = v&0x40 != 0
	a.frameStep = 0
	a.frameTimer = a.quarterFrameCycles
	if a.irqInhibit {
		// Inhibit set clears any pending IRQ + drops the line.
		a.frameIRQFlag = false
		if a.irqSink != nil {
			a.irqSink.ClearIRQSource(frameIRQSource)
		}
	}
	if !a.mode4Step {
		// 5-step mode: immediate quarter + half frame tick on write.
		a.tickQuarterFrame()
		a.tickHalfFrame()
	}
}

// Tick advances the APU by cpuCycles CPU cycles. Implements
// cpu.Ticker.
func (a *APU) Tick(cpuCycles int) {
	for range cpuCycles {
		a.stepCPU()
	}
}

// stepCPU is one CPU cycle worth of APU work: frame-counter step,
// pulse timer (every other cycle), sample emission accumulator.
func (a *APU) stepCPU() {
	a.frameTimer--
	if a.frameTimer <= 0 {
		a.advanceFrameStep()
	}
	if a.alternateTick {
		a.pulse1.tickTimer()
		a.pulse2.tickTimer()
		a.noise.tickTimer()
	}
	a.alternateTick = !a.alternateTick
	// Triangle period timer ticks every CPU cycle — its 32-step
	// sequencer needs the higher rate to reach audible
	// frequencies.
	a.triangle.tickTimer()
	// DMC period timer ticks every CPU cycle. The fetch path may
	// charge cpu.Stall cycles + assert IRQ at sample exhaustion.
	a.dmc.tickTimer(a.dmcBus, a.dmcStaller, a.irqSink)
	// Sunsoft 5B audio expansion (#306) — only present when the cart
	// is FME-7 with the audio half wired. Internally divides CPU
	// rate by 16 to match the YM2149 prescaler.
	if a.sunsoft5b != nil {
		a.sunsoft5b.Step()
	}
	// VRC6 audio expansion (#302) — only present when the cart is
	// VRC6 (mappers 24/26).
	if a.vrc6Audio != nil {
		a.vrc6Audio.Step()
	}

	// Sample emission. cyclesPerSample is fractional (40.585...);
	// accumulate in units of 1e6 to avoid drift over long horizons.
	const accumPerCycle = 1_000_000
	a.sampleAccum += accumPerCycle
	cyclesPerSample := a.cpuClockHz * 1_000_000 / SampleRate
	if a.sampleAccum >= cyclesPerSample {
		a.sampleAccum -= cyclesPerSample
		a.emitSample()
	}
}

// advanceFrameStep fires the right combination of quarter / half
// frame ticks for the current step + mode, then advances to the
// next step boundary.
func (a *APU) advanceFrameStep() {
	a.frameTimer += a.quarterFrameCycles
	if a.mode4Step {
		// 4-step pattern (q = quarter, h = half + quarter):
		//   step 0: q
		//   step 1: q + h
		//   step 2: q
		//   step 3: q + h + IRQ
		switch a.frameStep {
		case 0:
			a.tickQuarterFrame()
		case 1:
			a.tickQuarterFrame()
			a.tickHalfFrame()
		case 2:
			a.tickQuarterFrame()
		case 3:
			a.tickQuarterFrame()
			a.tickHalfFrame()
			// 4-step IRQ fires at the end of each cycle unless
			// inhibited. Level-triggered; stays asserted until
			// $4015 read or $4017-inhibit-set acks it.
			if !a.irqInhibit {
				a.frameIRQFlag = true
				if a.irqSink != nil {
					a.irqSink.AssertIRQSource(frameIRQSource)
				}
			}
		}
		a.frameStep = (a.frameStep + 1) & 3
	} else {
		// 5-step pattern: q, q+h, q, _, q+h. No IRQ. Step 3 is
		// idle on real silicon — slight difference vs 4-step.
		switch a.frameStep {
		case 0:
			a.tickQuarterFrame()
		case 1:
			a.tickQuarterFrame()
			a.tickHalfFrame()
		case 2:
			a.tickQuarterFrame()
		case 3:
			// idle
		case 4:
			a.tickQuarterFrame()
			a.tickHalfFrame()
		}
		a.frameStep = (a.frameStep + 1) % 5
	}
}

func (a *APU) tickQuarterFrame() {
	a.pulse1.tickEnvelope()
	a.pulse2.tickEnvelope()
	a.triangle.tickLinear()
	a.noise.tickEnvelope()
}

func (a *APU) tickHalfFrame() {
	a.pulse1.tickLength()
	a.pulse2.tickLength()
	a.pulse1.tickSweep()
	a.pulse2.tickSweep()
	a.triangle.tickLength()
	a.noise.tickLength()
}

// emitSample pushes one int16 sample into the ring buffer using a
// linear approximation of the nesdev DAC mixer. The output range
// is [-16384, 16384] roughly — well within int16 headroom for
// downstream mixing without clipping.
func (a *APU) emitSample() {
	if len(a.samples) >= a.samplesMax {
		// Drop oldest. Host should drain often enough that this is
		// rare; treat it as a backpressure signal rather than an
		// error.
		copy(a.samples, a.samples[1:])
		a.samples = a.samples[:len(a.samples)-1]
	}
	// Linear approximation: out = pulse1 + pulse2 + triangle.
	// Real silicon's mixer is non-linear (separate pulse_table +
	// tnd_table); #249 swaps in the LUT once noise + DMC land.
	// Triangle's amplitude (0-15) sits in the tnd group and on
	// real silicon mixes at a different coefficient; the linear
	// stand-in undercounts triangle slightly — fine for v0.3.
	// Non-linear DAC mix per nesdev (#249). Output is a float in
	// [0, ~1.0]; scale to int16 with headroom — peak combined
	// signal lands around 0.5 + 0.5 = 1.0, so 30000 keeps a
	// comfortable safety margin under int16 max.
	mix := mixSample(
		a.pulse1.output(),
		a.pulse2.output(),
		a.triangle.output(),
		a.noise.output(),
		a.dmc.mixerOutput(),
	)
	sample := int16(mix * 30000)
	// Sunsoft 5B expansion mix-in. The chip's Output() returns 0..45
	// (3 channels × 0..15 per square level). Scale to a small int16
	// addend so it sits alongside the 2A03 mix without clipping.
	if a.sunsoft5b != nil {
		sample += int16(a.sunsoft5b.Output() * 200)
	}
	// VRC6 expansion mix-in. Output range 0..61; scale similarly.
	if a.vrc6Audio != nil {
		sample += int16(a.vrc6Audio.Output() * 150)
	}
	// VRC7 OPLL mix-in (#315). Output() advances the FM synth one
	// sample + returns the summed carrier output (already scaled).
	// Called exactly once per emitted sample here.
	if a.vrc7Audio != nil {
		sample += int16(a.vrc7Audio.Output())
	}
	a.samples = append(a.samples, sample)
}

// Samples drains and returns the buffered samples. Host audio sink
// calls this each frame to feed Ebiten's audio context. Buffer is
// reset to empty after the drain so the next Tick batch fills from
// scratch.
func (a *APU) Samples() []int16 {
	out := make([]int16, len(a.samples))
	copy(out, a.samples)
	a.samples = a.samples[:0]
	return out
}

// compile-time checks.
var (
	_ cpu.Peripheral = (*APU)(nil)
	_ cpu.Ticker     = (*APU)(nil)
	_ cpu.Peripheral = (*StatusPeripheral)(nil)
)
