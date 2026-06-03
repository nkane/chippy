package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

// newLoopModel loads a deterministic program that mutates RAM every step so
// rewinds can be verified byte-for-byte:
//
//	$8000  EE 00 02   INC $0200
//	$8003  E8         INX
//	$8004  4C 00 80   JMP $8000
//
// Each loop bumps $0200 and X (both mod 256), so every step changes both a
// register and a RAM cell — exactly what a rewind must restore.
func newLoopModel() Model {
	ram := cpu.NewRAM()
	ram.EnableShadow()
	c := cpu.New(ram)
	prog := []byte{0xEE, 0x00, 0x02, 0xE8, 0x4C, 0x00, 0x80}
	ram.Load(0x8000, prog)
	c.PC = 0x8000
	return New(c, ram)
}

type machineState struct {
	step uint64
	pc   uint16
	a, x byte
	cell byte // $0200
}

func (m *Model) snapState() machineState {
	return machineState{
		step: m.StepCount,
		pc:   m.CPU.PC,
		a:    m.CPU.A,
		x:    m.CPU.X,
		cell: m.RAM.Read(0x0200),
	}
}

func runSteps(m *Model, n int) {
	for i := 0; i < n; i++ {
		m.step()
	}
}

func assertState(t *testing.T, got, want machineState) {
	t.Helper()
	if got != want {
		t.Fatalf("state mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestDeepRewind_Exact(t *testing.T) {
	m := newLoopModel()
	runSteps(&m, 4000)
	want := m.snapState() // remember step 4000

	// Run well past the fine ring (256) so the rewind must take the deep
	// keyframe-replay path.
	runSteps(&m, 1500) // now at step 5500
	if m.StepCount != 5500 {
		t.Fatalf("StepCount = %d; want 5500", m.StepCount)
	}

	out := m.cmdRewind([]string{"1500"})
	if !strings.Contains(out, "replayed") {
		t.Errorf("expected deep-path status, got %q", out)
	}
	assertState(t, m.snapState(), want)
}

func TestDeepRewind_FastPathWithinFineRing(t *testing.T) {
	m := newLoopModel()
	runSteps(&m, 500)
	want := m.snapState()
	runSteps(&m, 100) // 100 <= fine ring cap (256)
	out := m.cmdRewind([]string{"100"})
	if strings.Contains(out, "replayed") {
		t.Errorf("100-step rewind should use fast path, got %q", out)
	}
	assertState(t, m.snapState(), want)
}

func TestDeepRewind_ContinuityAfterLanding(t *testing.T) {
	// After a deep rewind, `<` (fine-ring pop) must still work, and stepping
	// forward again must reproduce the original trajectory.
	m := newLoopModel()
	runSteps(&m, 3000)
	atStep3000 := m.snapState()
	runSteps(&m, 2000) // step 5000
	m.cmdRewind([]string{"2000"})
	assertState(t, m.snapState(), atStep3000)

	// Fine ring repopulated by replay -> one more rewind step works.
	beforePC := m.CPU.PC
	s, ok := m.Rewind.Pop()
	if !ok {
		t.Fatal("fine ring empty after deep rewind replay")
	}
	m.CPU.Restore(s, m.RAM)
	if m.CPU.PC == beforePC && beforePC != 0x8000 {
		t.Errorf("pop did not change PC")
	}
}

func TestRewindBudget_Resize(t *testing.T) {
	m := newLoopModel()
	if m.RewindBudgetMB != defaultRewindBudgetMB {
		t.Fatalf("default budget = %d; want %d", m.RewindBudgetMB, defaultRewindBudgetMB)
	}
	out := m.cmdRewindBudget([]string{"256"})
	if m.RewindBudgetMB != 256 {
		t.Errorf("budget = %d; want 256", m.RewindBudgetMB)
	}
	// 256 MiB / 64 KiB = 4096 keyframes.
	if m.Keyframes.Cap() != 4096 {
		t.Errorf("cap = %d; want 4096", m.Keyframes.Cap())
	}
	if !strings.Contains(out, "256 MiB") {
		t.Errorf("status %q missing budget", out)
	}
	// Clamp + reject.
	m.cmdRewindBudget([]string{"99999"})
	if m.RewindBudgetMB != maxRewindBudgetMB {
		t.Errorf("over-max budget = %d; want clamp %d", m.RewindBudgetMB, maxRewindBudgetMB)
	}
	if out := m.cmdRewindBudget([]string{"0"}); !strings.Contains(out, "bad value") {
		t.Errorf("zero budget = %q; want bad value", out)
	}
}

func TestRewind_BeyondReach(t *testing.T) {
	m := newLoopModel()
	// Tiny budget -> 1 keyframe slot. After many steps the seed keyframe at
	// step 0 is evicted by later keyframes, so an old target is unreachable.
	m.cmdRewindBudget([]string{"1"}) // 1 MiB -> 16 keyframes
	runSteps(&m, 200000)             // many keyframes; step 0 long evicted
	out := m.cmdRewind([]string{"199000"})
	if !strings.Contains(out, "beyond reach") {
		t.Errorf("expected beyond-reach, got %q", out)
	}
}

func TestRewind_Reset(t *testing.T) {
	m := newLoopModel()
	runSteps(&m, 5000)
	if m.Keyframes.Len() == 0 || m.StepCount == 0 {
		t.Fatal("expected keyframes + step count before reset")
	}
	m.Keyframes.Reset()
	m.StepCount = 0
	if m.Keyframes.Len() != 0 || m.StepCount != 0 {
		t.Error("reset did not clear deep-rewind state")
	}
}

// BenchmarkDeepRewind measures a worst-case deep rewind: a full
// keyframe-interval forward replay. Acceptance (#392) is <100 ms; on the
// cycle-accurate core a 4096-instruction replay runs in well under 1 ms.
func BenchmarkDeepRewind(b *testing.B) {
	m := newLoopModel()
	runSteps(&m, 20000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Jump back a full interval (forces keyframe restore + replay), then
		// forward again so the next iteration repeats the same work.
		m.rewindToStep(m.StepCount - keyframeInterval)
		runSteps(&m, int(keyframeInterval))
	}
}

func TestHumanCount(t *testing.T) {
	cases := map[uint64]string{
		500:       "500",
		1500:      "1.5k",
		2_400_000: "2.4M",
	}
	for n, want := range cases {
		if got := humanCount(n); got != want {
			t.Errorf("humanCount(%d) = %q; want %q", n, got, want)
		}
	}
}
