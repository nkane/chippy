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

import "github.com/nkane/chippy/internal/cpu"

// Sample rate the APU's int16 ring buffer emits at. 44.1 kHz is the
// standard CD-quality target and the most common Ebiten audio
// context rate.
const SampleRate = 44100

// cpuClockHz is the NTSC CPU clock — 1.789773 MHz. cycles-per-sample
// at 44100 Hz comes out to 40.585...; the APU keeps a fractional
// accumulator so the emitted sample rate stays locked over time.
const cpuClockHz = 1789773

// quarterFrameCycles is the period of the 240 Hz frame-counter step
// in CPU cycles — 1789773 / 240 ≈ 7457. The 4-step mode fires four
// of these per frame counter cycle (q, h, q, h+IRQ).
const quarterFrameCycles = 7457

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
	pulse1 pulseChannel
	pulse2 pulseChannel

	// Frame counter state. mode4Step true = 4-step (the default,
	// 240 Hz IRQ ticks); false = 5-step (no IRQ). irqInhibit gates
	// the 4-step IRQ.
	mode4Step    bool
	irqInhibit   bool
	frameStep    int // 0-3 in 4-step, 0-4 in 5-step
	frameTimer   int // CPU cycles until the next step boundary
	frameIRQFlag bool

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
}

// New constructs an APU with the standard NTSC sample rate + a
// generously sized ring so a few frames of pending audio don't get
// dropped before the host drains them.
func New() *APU {
	pulse2 := pulseChannel{channelTwo: true}
	a := &APU{
		pulse2:    pulse2,
		mode4Step: true,
		// First quarter-frame fires at the 7457-cycle mark, not at
		// cycle 0. Initialize the timer so stepCPU drains down to
		// the boundary correctly.
		frameTimer: quarterFrameCycles,
		samplesMax: SampleRate / 4, // ~250 ms of buffered audio
	}
	a.samples = make([]int16, 0, a.samplesMax)
	return a
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
	// Triangle (bit 2), noise (bit 3), DMC (bit 4) bits land with
	// the channels themselves (v0.3 follow-ups). DMC IRQ at bit 7
	// also waits on the DMC channel.
	if a.frameIRQFlag {
		v |= 0x40
	}
	// Reading $4015 clears the frame-IRQ flag (NOT the DMC IRQ —
	// that has its own ack path).
	a.frameIRQFlag = false
	if a.irqSink != nil {
		a.irqSink.ClearIRQSource(frameIRQSource)
	}
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
	case 0x4015:
		a.pulse1.setEnabled(v&0x01 != 0)
		a.pulse2.setEnabled(v&0x02 != 0)
		// Triangle (bit 2), noise (bit 3), DMC (bit 4) enables are
		// accepted but no-op until v0.3.
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
	a.frameTimer = quarterFrameCycles
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
	}
	a.alternateTick = !a.alternateTick

	// Sample emission. cyclesPerSample is fractional (40.585...);
	// accumulate in units of 1e6 to avoid drift over long horizons.
	const accumPerCycle = 1_000_000
	a.sampleAccum += accumPerCycle
	cyclesPerSample := cpuClockHz * 1_000_000 / SampleRate
	if a.sampleAccum >= cyclesPerSample {
		a.sampleAccum -= cyclesPerSample
		a.emitSample()
	}
}

// advanceFrameStep fires the right combination of quarter / half
// frame ticks for the current step + mode, then advances to the
// next step boundary.
func (a *APU) advanceFrameStep() {
	a.frameTimer += quarterFrameCycles
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
}

func (a *APU) tickHalfFrame() {
	a.pulse1.tickLength()
	a.pulse2.tickLength()
	a.pulse1.tickSweep()
	a.pulse2.tickSweep()
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
	// Linear approximation: out = (p1 + p2) * scale.
	// Real silicon's pulse_table is non-linear, but linear is
	// audible-correct for v0.2 and avoids a 31-entry LUT.
	out := int32(a.pulse1.output()) + int32(a.pulse2.output())
	// Scale: each pulse is 0..15, so sum is 0..30. Map to ~0..15000
	// so int16 headroom remains for triangle + noise mixing later.
	sample := int16(out * 500)
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
