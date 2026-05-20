package apu

import (
	"math"
	"testing"
)

// Range is exactly $4000-$4013 — the channel-register window. $4014
// is OAMDMA territory; $4015 lives in the StatusPeripheral wrapper.
func TestAPU_Range(t *testing.T) {
	a := New()
	lo, hi := a.Range()
	if lo != 0x4000 || hi != 0x4013 {
		t.Fatalf("APU range = $%04X-$%04X; want $4000-$4013", lo, hi)
	}
	s := NewStatus(a)
	slo, shi := s.Range()
	if slo != 0x4015 || shi != 0x4015 {
		t.Fatalf("status range = $%04X-$%04X; want $4015-$4015", slo, shi)
	}
}

// Disabling a channel via $4015 clears its length counter — per
// nesdev wiki. The pulse-1-length-active bit (bit 0) of a $4015
// read then reads 0.
func TestAPU_Disable_ClearsLengthCounter(t *testing.T) {
	a := New()
	s := NewStatus(a)
	// Enable pulse 1, then set length via $4003. Real ROMs do
	// $4015 first because $4003's length load is gated on the
	// channel-enable bit at the time of write.
	s.Write(0x4015, 0x01)
	a.Write(0x4003, 0x08) // length idx 1 → LUT[1] = 254
	if got := s.Read(0x4015); got&0x01 == 0 {
		t.Fatalf("post-load pulse1 length bit = 0; want 1")
	}
	// Disable pulse 1 via $4015 bit 0 = 0.
	s.Write(0x4015, 0x00)
	if got := s.Read(0x4015); got&0x01 != 0 {
		t.Errorf("post-disable pulse1 length bit = 1; want 0")
	}
}

// $4017 frame counter forwarder: SetFrameCounter accepts bit 7
// (5-step mode) + bit 6 (IRQ inhibit). 5-step write also fires an
// immediate quarter+half tick, so a pre-loaded length counter
// decrements once.
func TestAPU_FrameCounter5StepImmediateTick(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x01)
	// envelopeLoop / lengthHalt must be CLEAR so the length counter
	// actually ticks down. $4000 default after New is zero (halt
	// clear) so we leave $4000 alone here.
	a.Write(0x4003, 0x08) // length LUT[1] = 254
	pre := a.pulse1.lengthCounter
	a.SetFrameCounter(0x80) // 5-step → immediate quarter + half tick
	if a.pulse1.lengthCounter != pre-1 {
		t.Errorf("5-step write didn't decrement length: %d → %d", pre, a.pulse1.lengthCounter)
	}
}

// Pulse output emits a square wave at the expected period. Set
// duty=50% + a known period + enable + length=non-zero; advance
// the APU enough cycles for several waveform cycles; count zero
// crossings in the sample buffer; assert frequency matches.
func TestPulse_GeneratesSquareWaveAtExpectedFrequency(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x01) // enable pulse 1 first so $4003 load takes
	// Configure pulse 1: duty 50% (bits 6-7 = 10), envelope
	// constant + max volume 15 = $BF.
	a.Write(0x4000, 0x9F)
	a.Write(0x4001, 0x00) // sweep off
	// Period = 200 (low + high). Frequency = 1789773 / (16 * (200+1)) ≈ 557 Hz.
	a.Write(0x4002, 0xC8)
	a.Write(0x4003, 0x08) // length idx 1 → 254 (long enough)

	// Advance 0.1 s of CPU time so the sample buffer fills up.
	a.Tick(cpuClockHz / 10)
	samples := a.Samples()
	if len(samples) < SampleRate/20 {
		t.Fatalf("sample buffer underfilled: %d samples", len(samples))
	}

	// Count low→high transitions (period count).
	transitions := 0
	prev := int16(0)
	for _, s := range samples {
		if prev <= 0 && s > 0 {
			transitions++
		}
		prev = s
	}
	// Expected frequency. f = CPU / (16 * (period + 1)).
	expectedHz := float64(cpuClockHz) / (16 * (200 + 1))
	durationS := float64(len(samples)) / float64(SampleRate)
	expectedTransitions := int(math.Round(expectedHz * durationS))
	// Allow ±10% tolerance for boundary effects + accumulator drift.
	tol := expectedTransitions / 10
	if tol < 2 {
		tol = 2
	}
	if transitions < expectedTransitions-tol || transitions > expectedTransitions+tol {
		t.Errorf("zero-crossings = %d; want %d ± %d (expected freq %.1f Hz, duration %.3f s)",
			transitions, expectedTransitions, tol, expectedHz, durationS)
	}
}

// Length counter silences the channel when it reaches zero. Load a
// small length, tick enough half-frames to drain it, observe
// silence in the sample buffer.
func TestPulse_LengthCounterSilences(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x01)
	a.Write(0x4000, 0x9F) // constant max volume
	a.Write(0x4002, 0x10)
	a.Write(0x4003, 0x10) // length LUT[2] = 20 ← small

	// Each half-frame fires every ~14914 CPU cycles. 20 length
	// counter ticks need ~20 half-frames = ~298000 cycles. Drain
	// well past that.
	a.Tick(400_000)
	if a.pulse1.lengthCounter != 0 {
		t.Fatalf("length counter = %d; want 0 after drain", a.pulse1.lengthCounter)
	}
	a.Samples() // drain any prior samples
	// Tick a bit more, expect silence.
	a.Tick(10_000)
	for _, sample := range a.Samples() {
		if sample != 0 {
			t.Errorf("got non-zero sample after length drain: %d", sample)
		}
	}
}

// Envelope decay: load envelope (constant flag off), tick quarter
// frames, watch decay counter drop from 15 toward 0.
func TestPulse_EnvelopeDecays(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x01)
	// Duty 50% + lengthHalt|envelopeLoop=off + constant flag OFF +
	// volume / envelope-period bits = 0 (fastest decay).
	a.Write(0x4000, 0x80) // duty 50%, constant=0, envelopeVolume=0
	a.Write(0x4002, 0xFF)
	a.Write(0x4003, 0xF8) // long length
	// Initial $4003 write primes envelope start; first quarter frame
	// reloads decay = 15.
	a.Tick(quarterFrameCycles + 10)
	if a.pulse1.envelopeDecay != 15 {
		t.Fatalf("post-first-tick decay = %d; want 15", a.pulse1.envelopeDecay)
	}
	// Tick five more quarter frames; with envelopeVolume=0 the
	// divider reloads to 0, so each quarter frame drops the decay
	// counter by 1.
	for range 5 {
		a.Tick(quarterFrameCycles)
	}
	if a.pulse1.envelopeDecay >= 15 {
		t.Errorf("decay didn't drop after 5 quarter frames: %d", a.pulse1.envelopeDecay)
	}
}

// Channel enable bit gates output. Disabled channel emits 0
// regardless of register state.
func TestPulse_DisabledChannelIsSilent(t *testing.T) {
	a := New()
	a.Write(0x4000, 0x9F)
	a.Write(0x4002, 0x80)
	a.Write(0x4003, 0x08)
	// $4015 NOT written → pulse 1 disabled by default.
	a.Tick(50_000)
	for _, s := range a.Samples() {
		if s != 0 {
			t.Fatalf("disabled pulse emitted non-zero sample: %d", s)
		}
	}
}

// Sweep mute: period < 8 silences the channel.
func TestPulse_PeriodTooSmallSilences(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x01)
	a.Write(0x4000, 0x9F)
	a.Write(0x4002, 0x07) // period low = 7
	a.Write(0x4003, 0x08) // period high = 0 → total period = 7 < 8
	a.Tick(50_000)
	for _, sample := range a.Samples() {
		if sample != 0 {
			t.Fatalf("period<8 didn't silence channel: sample=%d", sample)
		}
	}
}
