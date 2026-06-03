package trace

import "testing"

// repFromCycles builds a Replay whose frames carry the given cycle counts;
// PC is set to 0x8000+i so frames are individually identifiable.
func repFromCycles(cyc ...uint64) *Replay {
	r := &Replay{}
	for i, c := range cyc {
		r.Frames = append(r.Frames, Frame{PC: uint16(0x8000 + i), Cycles: c})
	}
	return r
}

func TestSeekCycle(t *testing.T) {
	r := repFromCycles(0, 7, 7, 12, 30, 100)
	cases := []struct {
		target uint64
		idx    int
		ok     bool
	}{
		{0, 0, true},
		{7, 1, true}, // first frame with Cycles >= 7
		{8, 3, true}, // skips the duplicate 7s to cyc=12
		{12, 3, true},
		{30, 4, true},
		{100, 5, true},
		{101, 5, false}, // past end -> last frame, false
	}
	for _, c := range cases {
		ok := r.SeekCycle(c.target)
		if r.Index != c.idx || ok != c.ok {
			t.Errorf("SeekCycle(%d) -> idx=%d ok=%v; want idx=%d ok=%v",
				c.target, r.Index, ok, c.idx, c.ok)
		}
	}
}

func TestSeekCycle_Empty(t *testing.T) {
	var r Replay
	if r.SeekCycle(5) {
		t.Error("SeekCycle on empty replay should be false")
	}
}

func TestFindFunc(t *testing.T) {
	r := repFromCycles(0, 1, 2, 3, 4, 5)
	r.Frames[2].A = 0x42
	r.Frames[4].A = 0x42
	pred := func(f Frame) bool { return f.A == 0x42 }

	// forward from 0 -> 2, then from 2 -> 4, then none.
	if i, ok := r.FindFunc(pred, 0, 1); !ok || i != 2 {
		t.Errorf("forward from 0: got %d,%v; want 2,true", i, ok)
	}
	if i, ok := r.FindFunc(pred, 2, 1); !ok || i != 4 {
		t.Errorf("forward from 2: got %d,%v; want 4,true", i, ok)
	}
	if _, ok := r.FindFunc(pred, 4, 1); ok {
		t.Error("forward from 4: expected no match")
	}
	// backward from 5 -> 4 -> 2.
	if i, ok := r.FindFunc(pred, 5, -1); !ok || i != 4 {
		t.Errorf("backward from 5: got %d,%v; want 4,true", i, ok)
	}
	if i, ok := r.FindFunc(pred, 4, -1); !ok || i != 2 {
		t.Errorf("backward from 4: got %d,%v; want 2,true", i, ok)
	}
	// FindFunc never moves the cursor.
	if r.Index != 0 {
		t.Errorf("FindFunc moved cursor to %d; want 0", r.Index)
	}
}

func TestDiff_FirstDivergence(t *testing.T) {
	a := repFromCycles(0, 5, 10, 15, 20)
	b := repFromCycles(0, 5, 10, 15, 20)
	if d := Diff(a, b); d.Found {
		t.Errorf("identical traces diverged: %+v", d)
	}
	// Perturb b at index 3.
	b.Frames[3].A = 0x99
	d := Diff(a, b)
	if !d.Found || d.Index != 3 || d.Cycle != 15 {
		t.Errorf("Diff = %+v; want Index=3 Cycle=15 Found=true", d)
	}
}

func TestDiff_LengthMismatch(t *testing.T) {
	a := repFromCycles(0, 5, 10, 15, 20) // longer
	b := repFromCycles(0, 5, 10)
	d := Diff(a, b)
	if !d.Found || d.Index != 3 || d.Cycle != 15 {
		t.Errorf("Diff length mismatch = %+v; want Index=3 Cycle=15 Found=true", d)
	}
}

func TestFrameEqual(t *testing.T) {
	f := Frame{PC: 0x8000, A: 1, X: 2, Y: 3, P: 4, SP: 5, Cycles: 6}
	g := f
	if !f.Equal(g) {
		t.Error("identical frames should be Equal")
	}
	g.Mnemonic = "different render" // derived field, must not affect Equal
	g.OpBytes = []byte{0xEA}
	g.InterruptIn = "NMI"
	if !f.Equal(g) {
		t.Error("derived/interrupt fields must not affect Equal")
	}
	g.Cycles = 7
	if f.Equal(g) {
		t.Error("cycle difference must break Equal")
	}
}
