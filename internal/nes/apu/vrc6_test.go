package apu

import "testing"

// Period registers commit through Write.
func TestVRC6Audio_PeriodCommit(t *testing.T) {
	v := NewVRC6Audio()
	v.Write(0x9001, 0x34) // pulse1 period low
	v.Write(0x9002, 0x81) // pulse1 period high (bit 0 = high nibble) + enable
	if v.pulse1.period != 0x134 {
		t.Errorf("pulse1 period = %d; want %d", v.pulse1.period, 0x134)
	}
	if !v.pulse1.enabled {
		t.Errorf("pulse1 not enabled")
	}
}

// Enabled pulse with non-zero volume emits non-zero output at some
// point during its duty cycle.
func TestVRC6Audio_PulseEmits(t *testing.T) {
	v := NewVRC6Audio()
	v.Write(0x9000, 0x4F) // duty=4, volume=15
	v.Write(0x9001, 0x02) // period low
	v.Write(0x9002, 0x80) // period high + enable
	saw := false
	for i := 0; i < 1000 && !saw; i++ {
		v.Step()
		if v.pulse1.output() > 0 {
			saw = true
		}
	}
	if !saw {
		t.Errorf("pulse never emitted non-zero output")
	}
}

// Disabled pulse stays silent.
func TestVRC6Audio_DisabledSilent(t *testing.T) {
	v := NewVRC6Audio()
	v.Write(0x9000, 0x0F) // vol 15 but channel disabled
	v.Write(0x9002, 0x00)
	for i := 0; i < 100; i++ {
		v.Step()
		if v.pulse1.output() != 0 {
			t.Fatalf("disabled pulse emitted %d", v.pulse1.output())
		}
	}
}

// Sawtooth accumulator builds up over time when enabled.
func TestVRC6Audio_SawtoothAccumulates(t *testing.T) {
	v := NewVRC6Audio()
	v.Write(0xB000, 0x10) // rate = 16
	v.Write(0xB001, 0x02)
	v.Write(0xB002, 0x80) // enable
	saw := false
	for i := 0; i < 1000 && !saw; i++ {
		v.Step()
		if v.saw.output() > 0 {
			saw = true
		}
	}
	if !saw {
		t.Errorf("sawtooth never emitted non-zero output")
	}
}
