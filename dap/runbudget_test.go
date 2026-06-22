package dap

import (
	"encoding/json"
	"testing"
)

// TestRunBudget_StopsOnBreakpoint verifies the synchronous server-driven run
// (issue #471) enforces an instruction breakpoint and reports the reason.
func TestRunBudget_StopsOnBreakpoint(t *testing.T) {
	// LDA #$00 ; LDA #$11 ; JMP $8000
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0xA9, 0x11, 0x4C, 0x00, 0x80})
	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"instructionReference":"$8002"}]}`),
	})

	stopped, reason, _ := s.RunBudget(100, func() { s.cpu.Step() }, nil)
	if !stopped || reason != "breakpoint" {
		t.Fatalf("want stopped breakpoint, got stopped=%v reason=%q", stopped, reason)
	}
	if s.cpu.PC != 0x8002 {
		t.Fatalf("want PC=$8002, got $%04X", s.cpu.PC)
	}
}

// TestRunBudget_StopsOnWatchpoint verifies a data breakpoint stops the run.
func TestRunBudget_StopsOnWatchpoint(t *testing.T) {
	// STA $0200 ; JMP $8003
	s, _, _ := newStoppedServer(t, []byte{0x8D, 0x00, 0x02, 0x4C, 0x03, 0x80})
	s.handleSetDataBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setDataBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"dataId":"$0200","accessType":"write"}]}`),
	})

	stopped, reason, _ := s.RunBudget(100, func() { s.cpu.Step() }, nil)
	if !stopped || reason != "data breakpoint" {
		t.Fatalf("want stopped data breakpoint, got stopped=%v reason=%q", stopped, reason)
	}
}

// TestRunBudget_StopAtPredicate verifies the caller stop predicate (used by
// step-over / run-to-line) fires and reports reason "step".
func TestRunBudget_StopAtPredicate(t *testing.T) {
	// NOP ; NOP ; NOP ; JMP $8003
	s, _, _ := newStoppedServer(t, []byte{0xEA, 0xEA, 0xEA, 0x4C, 0x03, 0x80})
	stopped, reason, _ := s.RunBudget(100, func() { s.cpu.Step() }, func() bool {
		return s.cpu.PC == 0x8002
	})
	if !stopped || reason != "step" {
		t.Fatalf("want stopped step at $8002, got stopped=%v reason=%q PC=$%04X", stopped, reason, s.cpu.PC)
	}
}

// TestRunBudget_ExhaustsBudget verifies a clean self-loop with no breakpoints
// runs to the budget and reports not-stopped.
func TestRunBudget_ExhaustsBudget(t *testing.T) {
	// NOP ; JMP $8000 — a tight loop that is NOT a self-targeting JMP, so the
	// JMP-self halt heuristic doesn't fire; with no breakpoint it runs out the
	// budget.
	s, _, _ := newStoppedServer(t, []byte{0xEA, 0x4C, 0x00, 0x80})
	stopped, _, _ := s.RunBudget(50, func() { s.cpu.Step() }, nil)
	if stopped {
		t.Fatalf("self-loop with no breakpoint should exhaust the budget, got stopped=%v", stopped)
	}
}
