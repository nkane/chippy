package apu

import "testing"

// Triangle enables via $4015 bit 2 and $400B loads its length
// counter from the 32-entry LUT. Reading $4015 reports the length
// in bit 2.
func TestTriangle_LengthLoadAndStatus(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x04)
	a.Write(0x400B, 0x08) // length idx 1 → 254
	if a.triangle.lengthCounter == 0 {
		t.Fatalf("$400B should have loaded length; lengthCounter = 0")
	}
	if got := s.Read(0x4015); got&0x04 == 0 {
		t.Errorf("$4015 bit 2 not set; got $%02X", got)
	}
	s.Write(0x4015, 0x00)
	if a.triangle.lengthCounter != 0 {
		t.Errorf("$4015 bit 2 clear should drain length")
	}
}

// Linear counter: $4008 sets reload value + control bit. $400B
// flags reload. Next quarter-frame tick reloads + (control=0)
// clears the flag so subsequent ticks decrement.
func TestTriangle_LinearCounterReloadAndDecrement(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x04)
	a.Write(0x4008, 0x10) // control=0, reload value=16
	a.Write(0x400B, 0x08) // sets reload flag
	if !a.triangle.linearReload {
		t.Fatalf("$400B did not set linearReload")
	}
	// One quarter frame → reload to 16, control=0 clears flag.
	a.Tick(quarterFrameCycles + 10)
	if a.triangle.linearCounter == 0 {
		t.Errorf("linearCounter = 0 after first q tick; want 16")
	}
	if a.triangle.linearReload {
		t.Errorf("linearReload should clear when control=0")
	}
	// Several more q-frame ticks → counter decrements toward 0.
	pre := a.triangle.linearCounter
	for range 4 {
		a.Tick(quarterFrameCycles)
	}
	if a.triangle.linearCounter >= pre {
		t.Errorf("linearCounter didn't decrement after 4 q ticks: pre=%d post=%d",
			pre, a.triangle.linearCounter)
	}
}

// Sequencer advances on timer underflow while length > 0 + linear >
// 0 + period >= 2. Single-step the timer enough times to verify
// the sequencer position walks.
func TestTriangle_SequencerAdvancesOnTimerTick(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x04)
	a.Write(0x4008, 0xFF) // control=1, reload=127 // control=1 (no decrement), reload=0
	// Smallest valid period (>= 2 to avoid the silence quirk).
	a.Write(0x400A, 0x02)
	a.Write(0x400B, 0x08) // length loaded
	// Tick enough quarter frames so the linear counter reloads.
	a.Tick(quarterFrameCycles + 10)
	if a.triangle.linearCounter == 0 {
		t.Fatalf("linearCounter still 0 after q reload; control bit didn't keep flag")
	}
	preStep := a.triangle.sequencerStep
	// Period 2 → underflow every ~3 CPU cycles. 100 cycles ≈ 33
	// advances → wraps the 32-step sequence around.
	a.Tick(100)
	if a.triangle.sequencerStep == preStep {
		t.Errorf("sequencer didn't advance after 100 CPU cycles at period 2")
	}
}

// Period < 2 silences the channel even with length + linear > 0.
func TestTriangle_PeriodTooSmallSilences(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x04)
	a.Write(0x4008, 0xFF)          // control=1, reload=127 // halt + control
	a.Write(0x400A, 0x01)          // period low = 1
	a.Write(0x400B, 0x08)          // period high = 0 → total = 1
	a.Tick(quarterFrameCycles * 2) // linear reload
	a.Samples()                    // drain
	a.Tick(10_000)
	for _, sample := range a.Samples() {
		if sample != 0 {
			t.Fatalf("period<2 didn't silence triangle: %d", sample)
		}
	}
}

// Disabled channel emits zero regardless of register state.
func TestTriangle_DisabledIsSilent(t *testing.T) {
	a := New()
	// $4015 NOT touched → triangle disabled by default.
	a.Write(0x4008, 0xFF) // control=1, reload=127
	a.Write(0x400A, 0x10)
	a.Write(0x400B, 0x08)
	a.Tick(50_000)
	for _, sample := range a.Samples() {
		if sample != 0 {
			t.Fatalf("disabled triangle emitted non-zero sample: %d", sample)
		}
	}
}

// Mixer includes triangle: a triangle-only channel at full volume
// produces non-zero samples while pulse channels are silent.
func TestTriangle_MixerContributes(t *testing.T) {
	a := New()
	s := NewStatus(a)
	s.Write(0x4015, 0x04) // triangle only
	a.Write(0x4008, 0xFF) // control=1, reload=127
	a.Write(0x400A, 0x80) // mid-range period
	a.Write(0x400B, 0x08)
	a.Tick(50_000)
	nonZero := 0
	for _, sample := range a.Samples() {
		if sample != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Errorf("triangle didn't contribute to mixer output")
	}
}
