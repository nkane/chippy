package cpu

import "testing"

func TestSnapshotFull_RoundTrip(t *testing.T) {
	ram := NewRAM()
	ram.EnableShadow()
	c := New(ram)
	for a := 0; a < 0x10000; a += 257 {
		ram.Data[a] = byte(a)
	}
	c.A, c.X, c.PC = 0x11, 0x22, 0x9000

	kf := c.SnapshotFull(ram)
	if len(kf.Pages) != 256 {
		t.Fatalf("SnapshotFull captured %d pages; want 256", len(kf.Pages))
	}
	// Mutate everything, then restore.
	for a := 0; a < 0x10000; a++ {
		ram.Data[a] = 0xEE
	}
	c.A, c.X, c.PC = 0, 0, 0
	c.Restore(kf, ram)
	if c.A != 0x11 || c.X != 0x22 || c.PC != 0x9000 {
		t.Errorf("regs not restored: A=%02X X=%02X PC=%04X", c.A, c.X, c.PC)
	}
	for a := 0; a < 0x10000; a += 257 {
		if ram.Data[a] != byte(a) {
			t.Fatalf("RAM[%04X] = %02X; want %02X", a, ram.Data[a], byte(a))
		}
	}
}

func TestKeyframeRing_CapFromBudget(t *testing.T) {
	if r := NewKeyframeRing(0); r != nil {
		t.Error("zero budget should yield nil ring")
	}
	// 64 MiB / 64 KiB = 1024.
	if r := NewKeyframeRing(64 << 20); r.Cap() != 1024 {
		t.Errorf("cap = %d; want 1024", r.Cap())
	}
	// Sub-keyframe budget still yields a 1-slot ring.
	if r := NewKeyframeRing(100); r.Cap() != 1 {
		t.Errorf("tiny budget cap = %d; want 1", r.Cap())
	}
}

func TestKeyframeRing_NearestAndEviction(t *testing.T) {
	r := NewKeyframeRing(3 * KeyframeBytes) // cap 3
	for _, step := range []uint64{0, 1000, 2000, 3000} {
		r.Push(Keyframe{Step: step})
	}
	// Cap 3 -> step 0 evicted; window is {1000,2000,3000}.
	if old, _ := r.Oldest(); old != 1000 {
		t.Errorf("oldest = %d; want 1000", old)
	}
	cases := []struct {
		target uint64
		step   uint64
		ok     bool
	}{
		{3500, 3000, true},
		{3000, 3000, true},
		{2999, 2000, true},
		{2000, 2000, true},
		{1000, 1000, true},
		{999, 0, false}, // older than the back of the window
	}
	for _, c := range cases {
		kf, ok := r.Nearest(c.target)
		if ok != c.ok || (ok && kf.Step != c.step) {
			t.Errorf("Nearest(%d) = (%d,%v); want (%d,%v)", c.target, kf.Step, ok, c.step, c.ok)
		}
	}
}

func TestKeyframeRing_Bytes(t *testing.T) {
	r := NewKeyframeRing(10 * KeyframeBytes)
	r.Push(Keyframe{Step: 0})
	r.Push(Keyframe{Step: 1})
	if got := r.Bytes(); got != 2*KeyframeBytes {
		t.Errorf("Bytes = %d; want %d", got, 2*KeyframeBytes)
	}
}

func TestKeyframeRing_NilSafe(t *testing.T) {
	var r *KeyframeRing
	r.Push(Keyframe{})
	if r.Len() != 0 || r.Cap() != 0 || r.Bytes() != 0 {
		t.Error("nil ring should report zero")
	}
	if _, ok := r.Nearest(5); ok {
		t.Error("nil ring Nearest should be false")
	}
}
