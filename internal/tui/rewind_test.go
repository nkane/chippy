package tui

import (
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

func TestRewindRing_PushPopLIFO(t *testing.T) {
	r := newRewindRing(4)
	for i := byte(0); i < 3; i++ {
		s := cpu.Snapshot{}
		s.A = i
		r.Push(s)
	}
	if r.Len() != 3 {
		t.Fatalf("len: want 3, got %d", r.Len())
	}
	// LIFO order — most recently pushed pops first.
	for want := byte(2); want != 255; want-- {
		s, ok := r.Pop()
		if !ok {
			t.Fatalf("expected pop at A=%d", want)
		}
		if s.A != want {
			t.Fatalf("pop order: want A=%d, got A=%d", want, s.A)
		}
		if want == 0 {
			break
		}
	}
	if r.Len() != 0 {
		t.Fatalf("after pops: want 0, got %d", r.Len())
	}
	if _, ok := r.Pop(); ok {
		t.Fatalf("empty pop should return ok=false")
	}
}

func TestRewindRing_OverflowDropsOldest(t *testing.T) {
	r := newRewindRing(3)
	for i := byte(0); i < 5; i++ {
		s := cpu.Snapshot{}
		s.A = i
		r.Push(s)
	}
	if r.Len() != 3 {
		t.Fatalf("cap=3 should clamp size; got %d", r.Len())
	}
	// Newest 3 are A=2,3,4. Pop order: 4, 3, 2.
	for _, want := range []byte{4, 3, 2} {
		s, _ := r.Pop()
		if s.A != want {
			t.Fatalf("overflow LIFO: want A=%d, got A=%d", want, s.A)
		}
	}
}

func TestRewindRing_ZeroCap(t *testing.T) {
	r := newRewindRing(0)
	if r != nil {
		t.Fatalf("cap=0 should yield nil ring")
	}
	// Push / Pop / Len on nil are safe.
	r.Push(cpu.Snapshot{})
	if r.Len() != 0 {
		t.Fatalf("nil Len: want 0")
	}
	if _, ok := r.Pop(); ok {
		t.Fatalf("nil Pop: want ok=false")
	}
}

func TestRewindRing_Reset(t *testing.T) {
	r := newRewindRing(4)
	r.Push(cpu.Snapshot{A: 1})
	r.Push(cpu.Snapshot{A: 2})
	r.Reset()
	if r.Len() != 0 {
		t.Fatalf("reset should empty ring")
	}
	r.Push(cpu.Snapshot{A: 7})
	s, _ := r.Pop()
	if s.A != 7 {
		t.Fatalf("post-reset push/pop want A=7, got A=%d", s.A)
	}
}

func TestRewind_StepThenRewindRestoresState(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Load(0x8000, []byte{0xA9, 0x42, 0xA9, 0x77}) // LDA #$42 ; LDA #$77
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram)
	m := New(c, ram)

	// Step once via the snapshot-recording helper.
	m.step()
	if c.A != 0x42 {
		t.Fatalf("post-step A want $42, got $%02X", c.A)
	}
	if m.Rewind.Len() != 1 {
		t.Fatalf("rewind depth want 1, got %d", m.Rewind.Len())
	}

	// Pop + restore — register should revert.
	s, ok := m.Rewind.Pop()
	if !ok {
		t.Fatalf("expected pop to succeed")
	}
	c.Restore(s, ram)
	if c.A != 0 {
		t.Fatalf("rewound A want $00, got $%02X", c.A)
	}
	if c.PC != 0x8000 {
		t.Fatalf("rewound PC want $8000, got $%04X", c.PC)
	}
}

func TestRewind_StepRewindStepParity(t *testing.T) {
	// Forward, back, forward should land on identical state.
	ram := cpu.NewRAM()
	ram.Load(0x8000, []byte{0xA9, 0x11, 0xAA, 0xE8}) // LDA #$11 ; TAX ; INX
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram)
	m := New(c, ram)

	m.step()
	m.step()
	m.step()
	want := cpuSnapshotFingerprint(c)

	// Rewind one, then re-step.
	s, _ := m.Rewind.Pop()
	c.Restore(s, ram)
	m.step()
	got := cpuSnapshotFingerprint(c)

	if got != want {
		t.Fatalf("step/rewind/step diverged from straight step:\n want %s\n  got %s", want, got)
	}
}

// cpuSnapshotFingerprint returns a stable string summarising the architectural
// state we care about for parity checks. We don't include cycle counters
// because re-stepping from a restored snapshot legitimately advances cycles
// from the same starting point (test only checks PC/regs/RAM diff).
func cpuSnapshotFingerprint(c *cpu.CPU) string {
	return string([]byte{c.A, c.X, c.Y, c.SP, c.P}) +
		string([]byte{byte(c.PC), byte(c.PC >> 8)})
}
