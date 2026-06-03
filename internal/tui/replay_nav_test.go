package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/trace"
)

func newReplayModel(frames ...trace.Frame) Model {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	m := New(c, ram)
	m.TraceReplay = &trace.Replay{Frames: frames}
	return m
}

func framesAB() []trace.Frame {
	return []trace.Frame{
		{PC: 0x8000, A: 0x00, Cycles: 0},
		{PC: 0x8002, A: 0x42, Cycles: 7},
		{PC: 0x8004, A: 0x10, Cycles: 12},
		{PC: 0x8006, A: 0x42, Cycles: 20},
		{PC: 0x8008, A: 0xFF, Cycles: 30},
	}
}

func TestCmdCycle(t *testing.T) {
	m := newReplayModel(framesAB()...)
	out := m.runCommand("cycle 12")
	if m.TraceReplay.Index != 2 {
		t.Errorf("index = %d; want 2 (%q)", m.TraceReplay.Index, out)
	}
	// CPU regs follow the landed frame.
	if m.CPU.PC != 0x8004 {
		t.Errorf("CPU.PC = $%04X; want $8004", m.CPU.PC)
	}
	// Between cycles -> first frame at/after.
	m.runCommand("cycle 8")
	if m.TraceReplay.Index != 2 {
		t.Errorf("cycle 8 index = %d; want 2", m.TraceReplay.Index)
	}
	// Past end -> last frame, status mentions "past end".
	if out := m.runCommand("cycle 999"); !strings.Contains(out, "past end") {
		t.Errorf("cycle 999 status = %q; want past end", out)
	}
}

func TestCmdCycle_BadInput(t *testing.T) {
	m := newReplayModel(framesAB()...)
	if out := m.runCommand("cycle"); !strings.Contains(out, "usage") {
		t.Errorf("got %q; want usage", out)
	}
	if out := m.runCommand("cycle abc"); !strings.Contains(out, "bad number") {
		t.Errorf("got %q; want bad number", out)
	}
}

func TestCmdFind_Forward(t *testing.T) {
	m := newReplayModel(framesAB()...)
	m.runCommand("find A=$42")
	if m.TraceReplay.Index != 1 {
		t.Fatalf("first find index = %d; want 1", m.TraceReplay.Index)
	}
	// Bare :find repeats the last expression -> next match at index 3.
	m.runCommand("find")
	if m.TraceReplay.Index != 3 {
		t.Fatalf("repeat find index = %d; want 3", m.TraceReplay.Index)
	}
	// No further match.
	if out := m.runCommand("find"); !strings.Contains(out, "no match") {
		t.Errorf("third find = %q; want no match", out)
	}
}

func TestCmdFind_Backward(t *testing.T) {
	m := newReplayModel(framesAB()...)
	m.TraceReplay.Index = 4
	m.runCommand("rfind A=$42")
	if m.TraceReplay.Index != 3 {
		t.Fatalf("rfind index = %d; want 3", m.TraceReplay.Index)
	}
	m.runCommand("rfind")
	if m.TraceReplay.Index != 1 {
		t.Fatalf("repeat rfind index = %d; want 1", m.TraceReplay.Index)
	}
}

func TestCmdFind_PCExpr(t *testing.T) {
	m := newReplayModel(framesAB()...)
	m.runCommand("find PC=$8006")
	if m.TraceReplay.Index != 3 {
		t.Errorf("find PC index = %d; want 3", m.TraceReplay.Index)
	}
}

func TestCmdFind_Errors(t *testing.T) {
	m := newReplayModel(framesAB()...)
	if out := m.runCommand("find"); !strings.Contains(out, "usage") {
		t.Errorf("bare find with no history = %q; want usage", out)
	}
	if out := m.runCommand("find @@@"); !strings.Contains(out, "find:") {
		t.Errorf("bad expr = %q; want find: error", out)
	}
}

func TestNormalizeFindExpr(t *testing.T) {
	cases := map[string]string{
		"PC=$8042":     "PC==$8042",
		"A=$42 && X=0": "A==$42 && X==0",
		"A==$42":       "A==$42", // already ==, untouched
		"A!=0":         "A!=0",
		"A>=$10":       "A>=$10",
		"A<=$10":       "A<=$10",
		"A>0 && B<5":   "A>0 && B<5", // no = at all
	}
	for in, want := range cases {
		if got := normalizeFindExpr(in); got != want {
			t.Errorf("normalizeFindExpr(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCmdFind_NotReplay(t *testing.T) {
	ram := cpu.NewRAM()
	m := New(cpu.New(ram), ram) // no TraceReplay
	if out := m.runCommand("find A=$00"); !strings.Contains(out, "not in trace-replay") {
		t.Errorf("got %q; want not-in-replay", out)
	}
}

func TestReplayDiff_Divergence(t *testing.T) {
	left := &trace.Replay{Frames: framesAB()}
	rightFrames := framesAB()
	rightFrames[3].A = 0x99 // perturb at index 3 (CYC:20)
	right := &trace.Replay{Frames: rightFrames}

	ram := cpu.NewRAM()
	m := New(cpu.New(ram), ram)
	m.TraceReplay = left
	m = m.WithReplayDiff(right)

	if !m.Diverge.Found || m.Diverge.Index != 3 || m.Diverge.Cycle != 20 {
		t.Fatalf("Diverge = %+v; want Index=3 Cycle=20 Found=true", m.Diverge)
	}
	if !strings.Contains(m.Status, "CYC:20") {
		t.Errorf("status %q missing divergence cycle", m.Status)
	}
	// diffModal renders the divergence header + frame data without panicking.
	view := m.diffModal(30)
	for _, want := range []string{"DIVERGE", "CYC:20", "8006", "L: -trace-replay", "R: -diff"} {
		if !strings.Contains(view, want) {
			t.Errorf("diffModal missing %q:\n%s", want, view)
		}
	}
}

func TestReplayDiff_Identical(t *testing.T) {
	left := &trace.Replay{Frames: framesAB()}
	right := &trace.Replay{Frames: framesAB()}
	ram := cpu.NewRAM()
	m := New(cpu.New(ram), ram)
	m.TraceReplay = left
	m = m.WithReplayDiff(right)
	if m.Diverge.Found {
		t.Errorf("identical traces: Diverge.Found = true")
	}
	if !strings.Contains(m.Status, "identical") {
		t.Errorf("status %q; want identical", m.Status)
	}
}
