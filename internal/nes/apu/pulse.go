package apu

// pulseChannel models a single NES square-wave channel ($4000-$4003
// for pulse 1, $4004-$4007 for pulse 2). The two channels are
// identical apart from a subtle one-bit difference in how the sweep
// unit negates the period: pulse 1 uses ones-complement (~p), pulse
// 2 uses twos-complement (-p). channelTwo selects the right mode.
type pulseChannel struct {
	enabled    bool
	channelTwo bool // selects pulse-2 sweep negate mode

	// Duty: bit pattern selector + position within the 8-step
	// waveform. Period elapses → dutyStep advances.
	duty     byte
	dutyStep byte

	// Period / timer. Timer counts down at the APU rate (half CPU);
	// when it underflows, it reloads from period and bumps dutyStep.
	// Period < 8 forces silence (real silicon's sweep-unit-mute path).
	timer  uint16
	period uint16

	// Length counter. Decremented every half-frame when not halted.
	// Channel silent while counter == 0 (irrespective of enabled).
	lengthHalt    bool
	lengthCounter byte

	// Envelope unit. Tracks the volume the channel actually emits.
	envelopeStart    bool
	envelopeLoop     bool // shares the lengthHalt bit ($4000 bit 5)
	envelopeConstant bool
	envelopeVolume   byte // 0..15 — either the constant volume or the envelope reload
	envelopeDivider  byte
	envelopeDecay    byte // current decayed level

	// Sweep unit. Adjusts the period at half-frame ticks.
	sweepEnabled bool
	sweepNegate  bool
	sweepShift   byte
	sweepPeriod  byte
	sweepDivider byte
	sweepReload  bool
}

// writeReg0 handles $4000 / $4004 — duty (bits 6-7), length halt
// + envelope loop (bit 5), envelope constant flag (bit 4), volume
// or envelope reload (bits 0-3).
func (p *pulseChannel) writeReg0(v byte) {
	p.duty = (v >> 6) & 0x03
	p.lengthHalt = v&0x20 != 0
	p.envelopeLoop = v&0x20 != 0
	p.envelopeConstant = v&0x10 != 0
	p.envelopeVolume = v & 0x0F
}

// writeReg1 handles $4001 / $4005 — sweep: enable (bit 7), period
// (bits 4-6), negate (bit 3), shift (bits 0-2). Writing this also
// flags the sweep divider for reload.
func (p *pulseChannel) writeReg1(v byte) {
	p.sweepEnabled = v&0x80 != 0
	p.sweepPeriod = (v >> 4) & 0x07
	p.sweepNegate = v&0x08 != 0
	p.sweepShift = v & 0x07
	p.sweepReload = true
}

// writeReg2 handles $4002 / $4006 — low 8 bits of period.
func (p *pulseChannel) writeReg2(v byte) {
	p.period = (p.period & 0xFF00) | uint16(v)
}

// writeReg3 handles $4003 / $4007 — high 3 bits of period (bits
// 0-2) and 5-bit length-counter reload (bits 3-7). Also restarts
// the duty position and primes the envelope.
func (p *pulseChannel) writeReg3(v byte) {
	p.period = (p.period & 0x00FF) | (uint16(v&0x07) << 8)
	if p.enabled {
		p.lengthCounter = lengthCounterLUT[v>>3]
	}
	p.dutyStep = 0
	p.envelopeStart = true
}

// tickTimer drops the period timer by one APU cycle. Returns true
// if the timer wrapped — caller advances dutyStep so the waveform
// can shift one position.
func (p *pulseChannel) tickTimer() bool {
	if p.timer == 0 {
		p.timer = p.period
		p.dutyStep = (p.dutyStep + 1) & 7
		return true
	}
	p.timer--
	return false
}

// tickEnvelope fires once per quarter-frame. Either restarts (after
// $400X write) or decays one step toward zero, with optional loop.
func (p *pulseChannel) tickEnvelope() {
	if p.envelopeStart {
		p.envelopeStart = false
		p.envelopeDecay = 15
		p.envelopeDivider = p.envelopeVolume
		return
	}
	if p.envelopeDivider > 0 {
		p.envelopeDivider--
		return
	}
	p.envelopeDivider = p.envelopeVolume
	if p.envelopeDecay > 0 {
		p.envelopeDecay--
	} else if p.envelopeLoop {
		p.envelopeDecay = 15
	}
}

// tickLength fires once per half-frame. Decrements the length
// counter unless the channel's halt bit is set or already zero.
func (p *pulseChannel) tickLength() {
	if !p.lengthHalt && p.lengthCounter > 0 {
		p.lengthCounter--
	}
}

// tickSweep fires once per half-frame. Computes the target period
// (current period ± shift), updates the live period if the divider
// elapsed, then reloads the divider.
func (p *pulseChannel) tickSweep() {
	if p.sweepDivider == 0 && p.sweepEnabled && p.sweepShift > 0 && !p.sweepMuted() {
		p.period = p.sweepTarget()
	}
	if p.sweepDivider == 0 || p.sweepReload {
		p.sweepDivider = p.sweepPeriod
		p.sweepReload = false
	} else {
		p.sweepDivider--
	}
}

// sweepTarget returns the period the sweep unit would land on if it
// updated right now. The two pulse channels differ in how negate
// works: pulse 1 subtracts shift, pulse 2 subtracts shift+1.
func (p *pulseChannel) sweepTarget() uint16 {
	delta := p.period >> p.sweepShift
	if p.sweepNegate {
		if p.channelTwo {
			return p.period - delta
		}
		return p.period - delta - 1
	}
	return p.period + delta
}

// sweepMuted reports whether the sweep unit currently mutes output
// — true when period < 8 (carry-prevention) or would-be target
// exceeds 11-bit range.
func (p *pulseChannel) sweepMuted() bool {
	return p.period < 8 || p.sweepTarget() > 0x7FF
}

// output is the channel's instantaneous sample in [0, 15]. Zero
// when silenced; otherwise the envelope volume gated by the duty
// waveform bit.
func (p *pulseChannel) output() byte {
	if !p.enabled || p.lengthCounter == 0 {
		return 0
	}
	if p.sweepMuted() {
		return 0
	}
	if dutyWaveforms[p.duty][p.dutyStep] == 0 {
		return 0
	}
	if p.envelopeConstant {
		return p.envelopeVolume
	}
	return p.envelopeDecay
}

// setEnabled mirrors $4015 bit-flip writes. Disabling a channel
// also clears its length counter (per nesdev).
func (p *pulseChannel) setEnabled(on bool) {
	p.enabled = on
	if !on {
		p.lengthCounter = 0
	}
}
