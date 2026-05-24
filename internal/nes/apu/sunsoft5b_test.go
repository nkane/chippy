package apu

import "testing"

// Writes to $C000 latch the register address; writes to $E000 set
// the register data.
func TestSunsoft5B_PortRouting(t *testing.T) {
	s := NewSunsoft5B()
	s.Write(0xC000, 8) // latch R8 (amplitude A)
	s.Write(0xE000, 0x0C)
	if s.regs[8] != 0x0C {
		t.Errorf("R8 = $%02X; want $0C", s.regs[8])
	}
	s.Write(0xC000, 7) // latch R7 (mixer)
	s.Write(0xE000, 0xFE)
	if s.regs[7] != 0xFE {
		t.Errorf("R7 = $%02X; want $FE", s.regs[7])
	}
}

// Tone period commits to the per-channel timer reload.
func TestSunsoft5B_TonePeriodCommit(t *testing.T) {
	s := NewSunsoft5B()
	// Period A = $0123 → low $23, high $01.
	s.Write(0xC000, 0)
	s.Write(0xE000, 0x23)
	s.Write(0xC000, 1)
	s.Write(0xE000, 0x01)
	if s.tones[0].period != 0x0123 {
		t.Errorf("tone A period = %d; want %d", s.tones[0].period, 0x0123)
	}
}

// A channel with tone enabled (mixer bit 0 clear) + amplitude > 0
// emits non-zero output. The output toggles between 0 and the
// amplitude as the timer expires.
func TestSunsoft5B_ToneOutputs(t *testing.T) {
	s := NewSunsoft5B()
	// Enable tone A by clearing bit 0 of R7 (default $FF = all off).
	s.Write(0xC000, 7)
	s.Write(0xE000, 0xFE)
	// Amplitude A = 12.
	s.Write(0xC000, 8)
	s.Write(0xE000, 0x0C)
	// Short period so toggles happen quickly.
	s.Write(0xC000, 0)
	s.Write(0xE000, 0x02)
	s.Write(0xC000, 1)
	s.Write(0xE000, 0x00)

	saw0, saw12 := false, false
	for i := 0; i < 10000 && (!saw0 || !saw12); i++ {
		s.Step()
		switch s.Output() {
		case 0:
			saw0 = true
		case 12:
			saw12 = true
		}
	}
	if !saw0 || !saw12 {
		t.Errorf("tone never toggled both extremes: saw0=%v saw12=%v", saw0, saw12)
	}
}

// Mixer bit set silences a channel even with amplitude > 0.
func TestSunsoft5B_MixerDisableSilences(t *testing.T) {
	s := NewSunsoft5B()
	s.Write(0xC000, 7)
	s.Write(0xE000, 0xFF) // all disabled
	s.Write(0xC000, 8)
	s.Write(0xE000, 0x0F)
	s.Write(0xC000, 0)
	s.Write(0xE000, 0x02)
	for i := 0; i < 1000; i++ {
		s.Step()
		if s.Output() != 0 {
			t.Fatalf("disabled channel emitted %d at step %d", s.Output(), i)
		}
	}
}
