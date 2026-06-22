package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

// TestRunBudget_LocalStopsOnBreakpoint exercises the server-driven local run
// (issue #471): a TUI breakpoint is forwarded to the in-process server, and
// RunBudget driven by m.step (which fills the rewind ring) stops at it.
func TestRunBudget_LocalStopsOnBreakpoint(t *testing.T) {
	ram := cpu.NewRAM()
	// LDA #$00 ; LDA #$11 ; JMP $8000
	prog := []byte{0xA9, 0x00, 0xA9, 0x11, 0x4C, 0x00, 0x80}
	ram.Load(0x8000, prog)
	c := cpu.New(ram)
	c.PC = 0x8000

	m := New(c, ram)
	m.Breakpoints[0x8002] = newBP(0x8002)
	m.syncSourceBPsAll()

	stopped, reason, _ := m.Source.RunBudget(1000, func() { m.step() }, nil)
	if !stopped || reason != "breakpoint" {
		t.Fatalf("want stopped breakpoint, got stopped=%v reason=%q", stopped, reason)
	}
	if m.CPU.PC != 0x8002 {
		t.Fatalf("want PC=$8002, got $%04X", m.CPU.PC)
	}
	// m.step fed the rewind ring during the run (rewind kept as the local
	// engine exception, issue #471).
	if m.Rewind == nil || m.Rewind.Len() == 0 {
		t.Fatalf("free-run via RunBudget(m.step) should fill the rewind ring")
	}
}

// TestRunBudget_LocalWatchpoint forwards a TUI memory watchpoint to the server
// and verifies the run stops on the watched write.
func TestRunBudget_LocalWatchpoint(t *testing.T) {
	ram := cpu.NewRAM()
	// STA $0200 ; JMP $8003
	ram.Load(0x8000, []byte{0x8D, 0x00, 0x02, 0x4C, 0x03, 0x80})
	c := cpu.New(ram)
	c.PC = 0x8000

	m := New(c, ram)
	m.MemBPs[0x0200] = newMemBP(0x0200, MemBPWrite)
	m.syncSourceBPsAll()

	stopped, reason, _ := m.Source.RunBudget(1000, func() { m.step() }, nil)
	if !stopped || reason != "data breakpoint" {
		t.Fatalf("want stopped data breakpoint, got stopped=%v reason=%q", stopped, reason)
	}
}
