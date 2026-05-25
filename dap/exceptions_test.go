package dap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExceptions_FilterAdvertised(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.handleInitialize(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "initialize",
		Arguments:       json.RawMessage(`{"adapterID":"chippy"}`),
	})
	body := out.String()
	if !strings.Contains(body, `"filter":"brk"`) {
		t.Fatalf("initialize should advertise brk filter, got: %s", body)
	}
	if !strings.Contains(body, `"supportsExceptionInfoRequest":true`) {
		t.Fatalf("initialize should advertise exceptionInfo support, got: %s", body)
	}
}

func TestExceptions_SetEnablesAndDisablesBRK(t *testing.T) {
	s, _, _ := newStoppedServer(t, nil)

	on := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setExceptionBreakpoints",
		Arguments:       json.RawMessage(`{"filters":["brk"]}`),
	}
	s.handleSetExceptionBreakpoints(on)
	if !s.brkOnException.Load() {
		t.Fatalf("brk filter set should turn brkOnException on")
	}

	off := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setExceptionBreakpoints",
		Arguments:       json.RawMessage(`{"filters":[]}`),
	}
	s.handleSetExceptionBreakpoints(off)
	if s.brkOnException.Load() {
		t.Fatalf("empty filter list should turn brkOnException off")
	}
}

func TestExceptions_RunStopsAtBRK(t *testing.T) {
	// LDA #$00 (2B) ; LDA #$11 (2B) ; BRK ($00) — non-degenerate path
	// to reach a BRK during free-run.
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0xA9, 0x11, 0x00})

	s.handleSetExceptionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setExceptionBreakpoints",
		Arguments:       json.RawMessage(`{"filters":["brk"]}`),
	})
	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	<-s.runDone
	if s.cpu.PC != 0x8004 {
		t.Fatalf("continue+brk-filter: want stop at $8004 (the BRK), got $%04X", s.cpu.PC)
	}
	if s.lastExceptionPC.Load() != 0x8004 {
		t.Fatalf("lastExceptionPC should be $8004, got $%04X", s.lastExceptionPC.Load())
	}
}

func TestExceptions_RunStillRunsWithoutFilter(t *testing.T) {
	// Same ROM but no filter set — run should execute past the BRK
	// straight into the IRQ vector (which is $0000 here), not pause.
	s, _, _ := newStoppedServer(t, []byte{0xEA, 0xEA, 0x4C, 0x00, 0x80})

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "continue",
	})
	// Loop is infinite — kill it after a beat.
	s.pauseRequested.Store(true)
	<-s.runDone
}

func TestExceptions_ExceptionInfo(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.lastExceptionPC.Store(0x9042)

	s.handleExceptionInfo(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "exceptionInfo",
	})
	body := out.String()
	if !strings.Contains(body, `"exceptionId":"brk"`) {
		t.Fatalf("exceptionInfo missing exceptionId: %s", body)
	}
	if !strings.Contains(body, `"BRK at $9042"`) {
		t.Fatalf("exceptionInfo description should reference PC: %s", body)
	}
}
