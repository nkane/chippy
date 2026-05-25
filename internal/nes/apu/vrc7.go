package apu

import "math"

// VRC7Audio is the Yamaha YM2413 (OPLL) FM-synth chip on Konami's
// VRC7 cart (mapper 85). Six melodic channels, each a 2-operator
// FM voice (modulator → carrier) with a per-operator ADSR envelope,
// driven from a 15-entry fixed instrument patch ROM plus one
// user-programmable patch. Lagrange Point is the only commercial
// NES VRC7 release; its soundtrack depends on this.
//
// Register interface (latched via the cart's $9010 address port +
// $9030 data port):
//
//	$00-$07 — user instrument patch (8 bytes)
//	$0E     — rhythm (unused on NES VRC7; ignored)
//	$10-$15 — channel F-number low 8 bits
//	$20-$25 — channel: F-number bit 8, block (octave), key-on, sustain
//	$30-$35 — channel: instrument select (high nibble) + volume (low)
//
// Fidelity notes: this is a functional FM synth, not a cycle-exact
// OPLL. Phase + envelope advance once per emitted audio sample
// (Output is called at SampleRate), using float sine FM rather than
// the chip's log/exp LUT pipeline. Modulator total level sets the
// modulation index; feedback is modelled on the modulator. KSL,
// vibrato/tremolo depth, and the exact ADSR rate tables are
// simplified — enough for an audible, recognisable rendition.
type VRC7Audio struct {
	regAddr byte
	regs    [64]byte
	ch      [6]opllChannel
}

type opllChannel struct {
	fnum     uint16 // 9-bit frequency number
	block    byte   // 0-7 octave
	inst     byte   // 0 = user patch, 1-15 = ROM
	volume   byte   // 0-15 (carrier attenuation; 0 = loudest)
	sustain  bool
	keyOn    bool
	mod, car opllOp
}

type opllOp struct {
	phase   float64 // current phase, radians
	env     float64 // envelope level 0..1
	stage   adsrStage
	lastOut float64 // for feedback on the modulator
}

type adsrStage int

const (
	adsrIdle adsrStage = iota
	adsrAttack
	adsrDecay
	adsrSustain
	adsrRelease
)

// NewVRC7Audio constructs an OPLL with all channels idle.
func NewVRC7Audio() *VRC7Audio { return &VRC7Audio{} }

// Write implements cart.VRC7AudioSink. $9010 latches the register
// address; $9030 commits data + applies it to channel state.
func (v *VRC7Audio) Write(addr uint16, val byte) {
	switch addr {
	case 0x9010:
		v.regAddr = val & 0x3F
	case 0x9030:
		v.regs[v.regAddr] = val
		v.apply(v.regAddr, val)
	}
}

func (v *VRC7Audio) apply(reg, val byte) {
	switch {
	case reg >= 0x10 && reg <= 0x15:
		c := &v.ch[reg-0x10]
		c.fnum = (c.fnum & 0x100) | uint16(val)
	case reg >= 0x20 && reg <= 0x25:
		c := &v.ch[reg-0x20]
		c.fnum = (c.fnum & 0x0FF) | (uint16(val&1) << 8)
		c.block = (val >> 1) & 0x07
		c.sustain = val&0x20 != 0
		newKey := val&0x10 != 0
		if newKey && !c.keyOn {
			c.keyOn = true
			c.mod.stage, c.car.stage = adsrAttack, adsrAttack
			c.mod.phase, c.car.phase = 0, 0
		} else if !newKey && c.keyOn {
			c.keyOn = false
			c.mod.stage, c.car.stage = adsrRelease, adsrRelease
		}
	case reg >= 0x30 && reg <= 0x35:
		c := &v.ch[reg-0x30]
		c.inst = (val >> 4) & 0x0F
		c.volume = val & 0x0F
	}
}

// patch returns the 8-byte instrument patch for a channel: the user
// patch ($00-$07) when inst==0, else the ROM entry.
func (v *VRC7Audio) patch(c *opllChannel) [8]byte {
	if c.inst == 0 {
		var p [8]byte
		copy(p[:], v.regs[0:8])
		return p
	}
	return opllPatchROM[c.inst]
}

// Output advances every channel one sample + returns the summed
// carrier output scaled to a small integer (matches the other
// expansions' Output() contract; folded into emitSample). Called
// once per emitted sample at SampleRate.
func (v *VRC7Audio) Output() int {
	var mix float64
	for i := range v.ch {
		mix += v.ch[i].step(v.patch(&v.ch[i]))
	}
	// Six carriers each in [-1,1]; scale so a full mix lands in a
	// sane int16 addend range alongside the 2A03 + other expansions.
	return int(mix * 1800)
}

// step advances both operators one sample + returns the carrier
// output in roughly [-1, 1]. Returns 0 while idle.
func (c *opllChannel) step(p [8]byte) float64 {
	if c.mod.stage == adsrIdle && c.car.stage == adsrIdle {
		return 0
	}
	// Base channel frequency (Hz) from F-number + block, per the
	// OPLL formula f = fnum * fsam / 2^(19-block), fsam ≈ 49716.
	const fsam = 49716.0
	baseHz := float64(c.fnum) * fsam / float64(uint32(1)<<(19-c.block))

	modMult := opllMult(p[0] & 0x0F)
	carMult := opllMult(p[1] & 0x0F)
	feedback := p[3] & 0x07
	modTL := float64(p[2]&0x3F) / 63.0 // 0 = max modulation depth

	// Modulator: self-feedback FM, attenuated by its total level.
	c.mod.advance(baseHz*modMult, modAttackRate(p[4]), modDecayRate(p[4]),
		sustainLevel(p[6]), releaseRate(p[6], c.sustain, c.keyOn))
	var fb float64
	if feedback > 0 {
		fb = c.mod.lastOut * float64(uint(1)<<feedback) / 64.0
	}
	modOut := math.Sin(c.mod.phase+fb) * c.mod.env
	c.mod.lastOut = modOut
	// Modulation index: a low modTL (0) = deep modulation.
	modIndex := (1.0 - modTL) * 6.0

	// Carrier: phase-modulated by the modulator, gated by the channel
	// volume + its envelope.
	c.car.advance(baseHz*carMult, modAttackRate(p[5]), modDecayRate(p[5]),
		sustainLevel(p[7]), releaseRate(p[7], c.sustain, c.keyOn))
	carOut := math.Sin(c.car.phase+modOut*modIndex) * c.car.env
	volAtten := 1.0 - float64(c.volume)/15.0
	return carOut * volAtten
}

// advance steps one operator: phase by its frequency, envelope by
// its ADSR stage. Rates are per-sample deltas (simplified vs the
// chip's exponential rate tables).
func (o *opllOp) advance(freqHz, ar, dr, sl, rr float64) {
	o.phase += 2 * math.Pi * freqHz / float64(SampleRate)
	if o.phase > 2*math.Pi {
		o.phase -= 2 * math.Pi
	}
	switch o.stage {
	case adsrAttack:
		o.env += ar
		if o.env >= 1 {
			o.env = 1
			o.stage = adsrDecay
		}
	case adsrDecay:
		o.env -= dr
		if o.env <= sl {
			o.env = sl
			o.stage = adsrSustain
		}
	case adsrSustain:
		// hold at sustain level
	case adsrRelease:
		o.env -= rr
		if o.env <= 0 {
			o.env = 0
			o.stage = adsrIdle
		}
	}
}

// opllMult maps the 4-bit MULT field to its frequency multiplier
// (the OPLL's half-integer multiples).
func opllMult(m byte) float64 {
	tab := [16]float64{0.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 10, 12, 12, 15, 15}
	return tab[m&0x0F]
}

// The following derive simplified per-sample ADSR deltas from the
// patch's 4-bit rate nibbles. Higher nibble → faster. These are not
// the chip's exact exponential rate tables; they give plausible
// attack/decay/release shapes.
func modAttackRate(b byte) float64 {
	r := (b >> 4) & 0x0F
	if r == 0 {
		return 0.0005
	}
	return 0.002 + float64(r)/15.0*0.4
}

func modDecayRate(b byte) float64 {
	r := b & 0x0F
	if r == 0 {
		return 0.00002
	}
	return 0.00005 + float64(r)/15.0*0.01
}

func sustainLevel(b byte) float64 {
	// High nibble of the SL/RR byte = sustain level (0 = loud,
	// 15 = quiet). Map to a 0..1 hold level.
	sl := (b >> 4) & 0x0F
	return 1.0 - float64(sl)/15.0
}

func releaseRate(b byte, sustain, keyOn bool) float64 {
	r := b & 0x0F
	base := 0.00005 + float64(r)/15.0*0.02
	// Percussive vs sustained release: a held sustain bit slows the
	// release once the key is up.
	if sustain && !keyOn {
		base *= 0.3
	}
	return base
}

// opllPatchROM is the YM2413 fixed-instrument table: 15 patches ×
// 8 bytes (index 0 unused — instrument 0 selects the user patch).
// Values are the widely-published OPLL instrument set.
var opllPatchROM = [16][8]byte{
	0:  {},
	1:  {0x03, 0x21, 0x05, 0x06, 0xE8, 0x81, 0x42, 0x27}, // Violin
	2:  {0x13, 0x41, 0x14, 0x0D, 0xD8, 0xF6, 0x23, 0x12}, // Guitar
	3:  {0x11, 0x11, 0x08, 0x08, 0xFA, 0xB2, 0x20, 0x12}, // Piano
	4:  {0x31, 0x61, 0x0C, 0x07, 0xA8, 0x64, 0x61, 0x27}, // Flute
	5:  {0x32, 0x21, 0x1E, 0x06, 0xE1, 0x76, 0x01, 0x28}, // Clarinet
	6:  {0x02, 0x01, 0x06, 0x00, 0xA3, 0xE2, 0xF4, 0xF4}, // Oboe
	7:  {0x21, 0x61, 0x1D, 0x07, 0x82, 0x81, 0x11, 0x07}, // Trumpet
	8:  {0x23, 0x21, 0x22, 0x17, 0xA2, 0x72, 0x01, 0x17}, // Organ
	9:  {0x35, 0x11, 0x25, 0x00, 0x40, 0x73, 0x72, 0x01}, // Horn
	10: {0xB5, 0x01, 0x0F, 0x0F, 0xA8, 0xA5, 0x51, 0x02}, // Synth
	11: {0x17, 0xC1, 0x24, 0x07, 0xF8, 0xF8, 0x22, 0x12}, // Harpsichord
	12: {0x71, 0x23, 0x11, 0x06, 0x65, 0x74, 0x18, 0x16}, // Vibraphone
	13: {0x01, 0x02, 0xD3, 0x05, 0xC9, 0x95, 0x03, 0x02}, // Synth bass
	14: {0x61, 0x63, 0x0C, 0x00, 0x94, 0xC0, 0x33, 0xF6}, // Acoustic bass
	15: {0x21, 0x72, 0x0D, 0x00, 0xC1, 0xD5, 0x56, 0x06}, // Electric guitar
}
