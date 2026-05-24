package apu

// triangleChannel models the NES triangle wave channel ($4008-$400B).
// Unlike the pulse channels it has no envelope or volume control —
// the waveform itself is the output. Linear counter + length counter
// gate the sequencer; both must be non-zero for the position to
// advance. Period timer ticks every CPU cycle (not every other like
// pulse), so audible-range frequencies stay reachable despite the
// 32-step sequence.
type triangleChannel struct {
	enabled bool

	// Linear counter unit. Clocked at quarter-frame ticks. The
	// reload flag is set by writes to $400B; cleared by the next
	// quarter-frame tick when the control bit is clear ("loop"
	// mode keeps the reload flag latched so the counter never
	// drains).
	linearReload  bool
	linearReloadV byte // value to reload from on next q tick
	linearControl bool // also doubles as length-halt
	linearCounter byte

	// Length counter — standard 32-entry LUT semantics, ticked on
	// half-frame. Halted by linearControl.
	lengthCounter byte

	// Period timer + 32-step sequencer. timer ticks every CPU
	// cycle; underflow reloads from period and bumps sequencerStep.
	// Period < 2 silences the channel (sequencer-position freeze
	// avoids the high-pitch buzz real silicon produces).
	timer         uint16
	period        uint16
	sequencerStep byte
}

// writeReg0 handles $4008 — bit 7 = linear-counter control / length
// halt; bits 0-6 = linear-counter reload value.
func (t *triangleChannel) writeReg0(v byte) {
	t.linearControl = v&0x80 != 0
	t.linearReloadV = v & 0x7F
}

// writeReg2 handles $400A — low 8 bits of period.
func (t *triangleChannel) writeReg2(v byte) {
	t.period = (t.period & 0xFF00) | uint16(v)
}

// writeReg3 handles $400B — high 3 bits of period (bits 0-2) +
// 5-bit length-counter reload (bits 3-7). Also sets the linear
// reload flag so the next quarter-frame tick re-primes the linear
// counter.
func (t *triangleChannel) writeReg3(v byte) {
	t.period = (t.period & 0x00FF) | (uint16(v&0x07) << 8)
	if t.enabled {
		t.lengthCounter = lengthCounterLUT[v>>3]
	}
	t.linearReload = true
}

// tickTimer drops the period timer by one CPU cycle. Returns true
// if the sequencer advanced.
func (t *triangleChannel) tickTimer() bool {
	if t.linearCounter == 0 || t.lengthCounter == 0 {
		return false // sequencer frozen; output holds last value
	}
	if t.period < 2 {
		// Real silicon emits an inaudible high-frequency hum here;
		// most emulators silence the channel and that's what v0.3
		// does too.
		return false
	}
	if t.timer == 0 {
		t.timer = t.period
		t.sequencerStep = (t.sequencerStep + 1) & 0x1F
		return true
	}
	t.timer--
	return false
}

// tickLinear runs on quarter-frame ticks. Either reloads from the
// stashed reload value or decrements toward zero; clears the
// reload flag unless the control bit (loop-mode) keeps it latched.
func (t *triangleChannel) tickLinear() {
	if t.linearReload {
		t.linearCounter = t.linearReloadV
	} else if t.linearCounter > 0 {
		t.linearCounter--
	}
	if !t.linearControl {
		t.linearReload = false
	}
}

// tickLength runs on half-frame ticks. Length-counter decrement
// gated by the length-halt bit (== linearControl on triangle).
func (t *triangleChannel) tickLength() {
	if !t.linearControl && t.lengthCounter > 0 {
		t.lengthCounter--
	}
}

// output is the current sequencer position's amplitude in [0, 15].
// Silent when length=0 OR linear=0 OR period<2 (matches the gating
// inside tickTimer).
func (t *triangleChannel) output() byte {
	if !t.enabled || t.lengthCounter == 0 || t.linearCounter == 0 || t.period < 2 {
		return 0
	}
	return triangleSequence[t.sequencerStep]
}

// setEnabled mirrors $4015 bit-2 writes. Disabling clears the
// length counter (per nesdev).
func (t *triangleChannel) setEnabled(on bool) {
	t.enabled = on
	if !on {
		t.lengthCounter = 0
	}
}
