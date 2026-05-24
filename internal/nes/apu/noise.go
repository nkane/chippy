package apu

// noiseChannel models the NES pseudo-random noise channel
// ($400C-$400F). A 15-bit LFSR clocks at one of 16 NTSC periods;
// output is the envelope volume gated by the LFSR's low bit. Drives
// percussion + ambient sounds (SMB1 footsteps + breaking blocks,
// Zelda sword swings, Metroid lasers).
//
// Two LFSR modes per $400E bit 7:
//   - mode 0 (long): 32767-step period; feedback = bit0 XOR bit1.
//   - mode 1 (short): 93-step period;  feedback = bit0 XOR bit6.
type noiseChannel struct {
	enabled bool

	// Envelope unit — identical semantics to pulse. lengthHalt
	// also doubles as envelopeLoop, matching $400C bit 5.
	lengthHalt       bool
	envelopeLoop     bool
	envelopeConstant bool
	envelopeVolume   byte
	envelopeStart    bool
	envelopeDivider  byte
	envelopeDecay    byte

	// Length counter — standard 32-entry LUT semantics, half-frame
	// tick, halted by lengthHalt.
	lengthCounter byte

	// LFSR + timing. shortMode toggles the 93-step variant.
	timer     uint16
	period    uint16
	lfsr      uint16
	shortMode bool
}

// writeReg0 handles $400C — bit 5 = length halt / envelope loop,
// bit 4 = envelope constant flag, bits 0-3 = envelope volume /
// reload value.
func (n *noiseChannel) writeReg0(v byte) {
	n.lengthHalt = v&0x20 != 0
	n.envelopeLoop = v&0x20 != 0
	n.envelopeConstant = v&0x10 != 0
	n.envelopeVolume = v & 0x0F
}

// writeReg2 handles $400E — bit 7 = LFSR mode (long/short), bits
// 0-3 = period index into the NTSC LUT.
func (n *noiseChannel) writeReg2(v byte) {
	n.shortMode = v&0x80 != 0
	n.period = noisePeriodLUT[v&0x0F]
}

// writeReg3 handles $400F — bits 3-7 = length counter LUT index.
// Also primes the envelope start.
func (n *noiseChannel) writeReg3(v byte) {
	if n.enabled {
		n.lengthCounter = lengthCounterLUT[v>>3]
	}
	n.envelopeStart = true
}

// tickTimer drops the period timer by one APU cycle. When 0,
// reload from period and clock the LFSR.
func (n *noiseChannel) tickTimer() {
	if n.timer == 0 {
		n.timer = n.period
		n.clockLFSR()
		return
	}
	n.timer--
}

// clockLFSR shifts the 15-bit register right and feeds the
// computed bit back into bit 14. Feedback bit depends on mode.
func (n *noiseChannel) clockLFSR() {
	if n.lfsr == 0 {
		// Real silicon's reset latches LFSR to 1; defensive guard
		// for headless tests that poke the struct directly.
		n.lfsr = 1
	}
	var feedbackBit uint16
	if n.shortMode {
		feedbackBit = (n.lfsr ^ (n.lfsr >> 6)) & 1
	} else {
		feedbackBit = (n.lfsr ^ (n.lfsr >> 1)) & 1
	}
	n.lfsr = (n.lfsr >> 1) | (feedbackBit << 14)
}

// tickEnvelope fires once per quarter-frame. Same algorithm as
// pulse's envelope.
func (n *noiseChannel) tickEnvelope() {
	if n.envelopeStart {
		n.envelopeStart = false
		n.envelopeDecay = 15
		n.envelopeDivider = n.envelopeVolume
		return
	}
	if n.envelopeDivider > 0 {
		n.envelopeDivider--
		return
	}
	n.envelopeDivider = n.envelopeVolume
	if n.envelopeDecay > 0 {
		n.envelopeDecay--
	} else if n.envelopeLoop {
		n.envelopeDecay = 15
	}
}

// tickLength fires once per half-frame. Halted by lengthHalt.
func (n *noiseChannel) tickLength() {
	if !n.lengthHalt && n.lengthCounter > 0 {
		n.lengthCounter--
	}
}

// output is the channel's amplitude in [0, 15]. Silent when length
// counter has drained or LFSR bit 0 is set (the inverse-output
// gating real silicon performs).
func (n *noiseChannel) output() byte {
	if !n.enabled || n.lengthCounter == 0 {
		return 0
	}
	if n.lfsr&1 != 0 {
		return 0
	}
	if n.envelopeConstant {
		return n.envelopeVolume
	}
	return n.envelopeDecay
}

// setEnabled mirrors $4015 bit-3 writes. Disabling clears length.
func (n *noiseChannel) setEnabled(on bool) {
	n.enabled = on
	if !on {
		n.lengthCounter = 0
	}
}
