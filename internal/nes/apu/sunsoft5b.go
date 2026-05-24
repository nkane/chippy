package apu

// Sunsoft5B is the audio half of the FME-7 / Sunsoft 5B mapper
// package (mapper 69) — a YM2149 (Yamaha PSG / "SSG") clone with
// three square-wave channels + amplitude control + noise generator
// + envelope generator. Gimmick! is the headliner.
//
// Cart-side register interface (forwarded from FME-7's $C000/$E000
// port pair via cart.Sunsoft5BSink):
//
//	$C000 — address latch (0..15)
//	$E000 — data write to the latched register
//
// Register file:
//
//	R0/R1   — tone period A (low 8 + high 4 bits → 12-bit period)
//	R2/R3   — tone period B
//	R4/R5   — tone period C
//	R6      — noise period (5 bits)
//	R7      — mixer (bit n = disable tone n, bit n+3 = disable noise n;
//	          inverted: 0 = enabled, 1 = disabled)
//	R8-R10  — amplitude per channel (bit 4 = envelope follow,
//	          bits 0-3 = fixed level)
//	R11/R12 — envelope period (16-bit)
//	R13     — envelope shape (4 bits — continue / attack / alt / hold)
//	R14/R15 — I/O ports (unused on NES — silent no-op)
//
// Out of scope (v0.6 minimum-viable):
//   - Noise generator's LFSR (the noise period register is captured
//     but the channel's output is treated as silent — Gimmick!
//     doesn't lean heavily on noise).
//   - Exotic envelope shapes; we ship sawtooth (shape 0xE) +
//     triangle (shape 0xA) which cover the shipping-ROM usage.
type Sunsoft5B struct {
	regAddr byte
	regs    [16]byte

	tones [3]ssgTone

	envelopeCounter int
	envelopeStep    int  // 0..31 — index into the active shape
	envelopeShape   byte // R13 latch
	envelopeHold    bool
}

type ssgTone struct {
	timer  int
	period int  // 12-bit reload (1..4096)
	output bool // current square level
}

// NewSunsoft5B constructs a Sunsoft 5B chip in its power-on state
// (silent — all tone channels disabled via mixer bit 7).
func NewSunsoft5B() *Sunsoft5B {
	s := &Sunsoft5B{}
	s.regs[7] = 0xFF // all channels disabled at power-on
	return s
}

// Write implements cart.Sunsoft5BSink. The FME-7 cart forwards
// CPU writes to its $C000 / $E000 address+data port pair here so
// the audio chip stays decoupled from the cart's bank-switching
// surface.
func (s *Sunsoft5B) Write(addr uint16, v byte) {
	switch {
	case addr >= 0xC000 && addr < 0xE000:
		s.regAddr = v & 0x0F
	case addr >= 0xE000:
		s.regs[s.regAddr] = v
		s.commit(s.regAddr)
	}
}

// commit propagates a freshly-written register into the running
// state (period reloads, envelope shape latch).
func (s *Sunsoft5B) commit(reg byte) {
	switch reg {
	case 0, 1:
		s.tones[0].period = int(s.regs[0]) | int(s.regs[1]&0x0F)<<8
	case 2, 3:
		s.tones[1].period = int(s.regs[2]) | int(s.regs[3]&0x0F)<<8
	case 4, 5:
		s.tones[2].period = int(s.regs[4]) | int(s.regs[5]&0x0F)<<8
	case 13:
		s.envelopeShape = s.regs[13] & 0x0F
		s.envelopeStep = 0
		s.envelopeCounter = 0
		s.envelopeHold = false
	}
}

// Step advances the chip by one CPU cycle. Tone counters tick at
// (CPU clock / 16) — YM2149 internal prescaler — so we run a
// modulo-16 divider.
//
// The "real" PSG clocks at half the NES CPU rate then divides by
// 8 internally (16 total). We collapse the two into a single mod-
// 16 divider — sample timing is per-CPU-cycle elsewhere so the
// effective tone frequency lands within ~1% of silicon.
var ssgDivider int

func (s *Sunsoft5B) Step() {
	ssgDivider++
	if ssgDivider < 16 {
		return
	}
	ssgDivider = 0
	for i := range s.tones {
		t := &s.tones[i]
		if t.period == 0 {
			continue
		}
		t.timer--
		if t.timer <= 0 {
			t.timer = t.period
			t.output = !t.output
		}
	}
	// Envelope generator runs at the same divided rate.
	s.stepEnvelope()
}

// stepEnvelope advances the envelope counter + walks the shape's
// step table. Only sawtooth-down (shape 0xA / 0xE) is modelled;
// the rest hold at the start value.
func (s *Sunsoft5B) stepEnvelope() {
	period := int(s.regs[11]) | int(s.regs[12])<<8
	if period == 0 {
		return
	}
	s.envelopeCounter++
	if s.envelopeCounter < period {
		return
	}
	s.envelopeCounter = 0
	if s.envelopeHold {
		return
	}
	switch s.envelopeShape {
	case 0x0A: // alternate (triangle)
		// /\/\... 0..15..0..15..
		s.envelopeStep = (s.envelopeStep + 1) & 0x1F
	case 0x0E: // saw continue
		// 0..15..0..15..
		s.envelopeStep = (s.envelopeStep + 1) & 0x0F
	default:
		// Other shapes either hold at start or are uncommonly used
		// by shipping ROMs. Step counter increments + clamps at 15.
		if s.envelopeStep < 15 {
			s.envelopeStep++
		} else {
			s.envelopeHold = true
		}
	}
}

// envelopeLevel returns the envelope generator's current 4-bit
// amplitude (0..15). Maps the 32-step alternate position to a
// 0..15 triangle.
func (s *Sunsoft5B) envelopeLevel() byte {
	if s.envelopeShape == 0x0A {
		if s.envelopeStep < 16 {
			return byte(s.envelopeStep)
		}
		return byte(31 - s.envelopeStep)
	}
	return byte(s.envelopeStep & 0x0F)
}

// Output sums the three tone channels at their current level. Each
// channel emits a square wave between 0 and its amplitude when its
// mixer bit is enabled. Result is a small positive integer (0..45,
// since each channel contributes 0..15).
func (s *Sunsoft5B) Output() int {
	mixer := s.regs[7]
	out := 0
	for i := range s.tones {
		if mixer&(1<<i) != 0 {
			continue // tone disabled
		}
		amp := s.regs[8+i] & 0x1F
		var level byte
		if amp&0x10 != 0 {
			level = s.envelopeLevel()
		} else {
			level = amp & 0x0F
		}
		if s.tones[i].output {
			out += int(level)
		}
	}
	return out
}
