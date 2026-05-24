package apu

import "testing"

// Range covers the channel's register window only. $4015 stays on
// StatusPeripheral; noise just contributes a bit to the status
// byte.
func TestNoise_EnableAndStatus(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x08) // noise enable
	a.Write(0x400F, 0x10) // length LUT[2] = 20
	if a.noise.lengthCounter == 0 {
		t.Fatalf("$400F should have loaded length; got 0")
	}
	if got := s.Read(0x4015); got&0x08 == 0 {
		t.Errorf("$4015 bit 3 not set; got $%02X", got)
	}
	s.Write(0x4015, 0x00)
	if a.noise.lengthCounter != 0 {
		t.Errorf("$4015 bit 3 clear should drain length")
	}
}

// LFSR mode 0 (long) period is 32767 steps. We don't simulate the
// full cycle (slow); instead assert the LFSR walks through several
// distinct states after enough clocks.
func TestNoise_LFSRLongModeProducesDistinctStates(t *testing.T) {
	a := New()
	// Set period 0 ($400E bits 0-3 = 0) → tightest LUT entry (4
	// CPU cycles per clock; ~every other apu tick = 2 apu cycles).
	a.Write(0x400E, 0x00) // long mode + period idx 0
	a.noise.period = noisePeriodLUT[0]
	a.noise.lfsr = 1

	// Take a snapshot of the first 32 LFSR states.
	seen := map[uint16]struct{}{}
	for range 32 {
		seen[a.noise.lfsr] = struct{}{}
		a.noise.clockLFSR()
	}
	if len(seen) < 20 {
		t.Errorf("long-mode LFSR cycled too quickly: only %d distinct states in 32 clocks", len(seen))
	}
}

// LFSR mode 1 (short) has a 93-step period. Within 100 clocks
// every emitted state should have repeated at least once.
func TestNoise_LFSRShortModeHas93StepPeriod(t *testing.T) {
	a := New()
	a.Write(0x400E, 0x80) // short mode
	a.noise.lfsr = 1
	first := a.noise.lfsr
	for i := 1; i <= 100; i++ {
		a.noise.clockLFSR()
		if a.noise.lfsr == first {
			if i < 90 || i > 95 {
				t.Errorf("short-mode period = %d clocks; want ~93", i)
			}
			return
		}
	}
	t.Errorf("short-mode LFSR didn't loop within 100 clocks")
}

// Length counter silences after drain. Half-frame ticks decrement
// when lengthHalt is clear; with $400C bit 5 = 0 the counter ticks
// down.
func TestNoise_LengthCounterSilences(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x08)
	a.Write(0x400C, 0x1F) // constant=1, vol=15, halt=0
	a.Write(0x400E, 0x04) // period idx 4 (period 64)
	a.Write(0x400F, 0x10) // length LUT[2] = 20
	a.Tick(400_000)       // ~20 half-frames worth
	if a.noise.lengthCounter != 0 {
		t.Fatalf("length counter = %d after drain; want 0", a.noise.lengthCounter)
	}
	a.Samples()
	a.Tick(10_000)
	for _, sample := range a.Samples() {
		if sample != 0 {
			t.Errorf("post-drain non-zero sample: %d", sample)
		}
	}
}

// Envelope decays when constant flag is clear.
func TestNoise_EnvelopeDecays(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x08)
	a.Write(0x400C, 0x00) // constant=0, vol=0 (fastest decay), halt=0
	a.Write(0x400E, 0x04)
	a.Write(0x400F, 0xF8) // long length so envelope can decay
	// First q tick reloads decay=15.
	a.Tick(quarterFrameCycles + 10)
	if a.noise.envelopeDecay != 15 {
		t.Fatalf("post-first-tick decay = %d; want 15", a.noise.envelopeDecay)
	}
	for range 5 {
		a.Tick(quarterFrameCycles)
	}
	if a.noise.envelopeDecay >= 15 {
		t.Errorf("decay didn't drop after 5 q ticks: %d", a.noise.envelopeDecay)
	}
}

// Disabled channel emits zero.
func TestNoise_DisabledIsSilent(t *testing.T) {
	a := New()
	a.Write(0x400C, 0x1F)
	a.Write(0x400E, 0x04)
	a.Write(0x400F, 0x08)
	a.Tick(50_000)
	for _, sample := range a.Samples() {
		if sample != 0 {
			t.Fatalf("disabled noise emitted non-zero sample: %d", sample)
		}
	}
}

// Mixer includes noise: noise-only with full volume produces
// non-zero samples while pulse + triangle stay silent.
func TestNoise_MixerContributes(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x08) // noise only
	a.Write(0x400C, 0x1F) // constant=1, vol=15, halt=0
	a.Write(0x400E, 0x04) // mid period
	a.Write(0x400F, 0xF8) // long length
	a.Tick(50_000)
	nonZero := 0
	for _, sample := range a.Samples() {
		if sample != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Errorf("noise didn't contribute to mixer output")
	}
}
