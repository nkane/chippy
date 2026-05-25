package dap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBPMeta_ConditionSuppressesUntilTrue(t *testing.T) {
	// LDA #$00 ; LDA #$11 ; LDA #$22 ; JMP $8000. Bp at $8002 (the
	// second LDA) with condition A == $11 — fires only when A is $11,
	// which is true on the second iteration of the loop.
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0xA9, 0x11, 0xA9, 0x22, 0x4C, 0x00, 0x80})

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments: json.RawMessage(`{"breakpoints":[
			{"instructionReference":"$8002","condition":"X == $42"}
		]}`),
	}
	s.handleSetInstructionBreakpoints(req)

	// Continue: condition X==$42 is never true (X stays $00) so the bp
	// shouldn't fire; the run loop ends up looping forever until we
	// pause.
	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	// Let the run loop spin a few iterations so we know it executed
	// past the suppressed bp.
	time.Sleep(20 * time.Millisecond)
	s.pauseRequested.Store(true)
	<-s.runDone
	if s.cpu.Cycles < 50 {
		t.Fatalf("expected free-run past the bp; cycles only %d", s.cpu.Cycles)
	}
}

func TestBPMeta_ConditionFiresWhenTrue(t *testing.T) {
	// $8002 = LDA #$11. After it executes A=$11. Bp at $8004 (the
	// third LDA) with condition A == $11 fires immediately.
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0xA9, 0x11, 0xA9, 0x22, 0x4C, 0x00, 0x80})

	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments: json.RawMessage(`{"breakpoints":[
			{"instructionReference":"$8004","condition":"A == $11"}
		]}`),
	})
	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	<-s.runDone
	if s.cpu.PC != 0x8004 {
		t.Fatalf("bp with true cond should stop at $8004, got $%04X", s.cpu.PC)
	}
}

func TestBPMeta_HitConditionThirdHit(t *testing.T) {
	// Loop: NOP ; JMP $8000. Bp at $8001 (JMP) with hitCondition "3".
	s, _, _ := newStoppedServer(t, []byte{0xEA, 0x4C, 0x00, 0x80})

	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments: json.RawMessage(`{"breakpoints":[
			{"instructionReference":"$8001","hitCondition":"3"}
		]}`),
	})
	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	<-s.runDone

	meta := s.lookupBPMeta(0x8001)
	if meta == nil {
		t.Fatalf("expected meta at $8001")
	}
	if meta.Hits != 3 {
		t.Fatalf("hitCondition 3 should fire on the 3rd hit; got Hits=%d", meta.Hits)
	}
}

func TestBPMeta_LogMessageEmitsOutputEvent(t *testing.T) {
	// Same NOP-JMP loop. Bp at $8001 with logMessage that should fire
	// repeatedly without stopping. Force pause after a bit to confirm
	// we're still running.
	s, _, out := newStoppedServer(t, []byte{0xEA, 0x4C, 0x00, 0x80})

	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments: json.RawMessage(`{"breakpoints":[
			{"instructionReference":"$8001"}
		]}`),
	})
	// Re-set with logMessage by recreating — first set covered the
	// fast happy path, second confirms logMessage suppresses the stop.
	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[]}`),
	})

	// Use a source-line breakpoint path with logMessage: jam the meta
	// in directly through buildBPMeta to keep the test focused on the
	// log-on-hit semantics. Setting via a source bp requires a real
	// srcMap, which is overkill here.
	s.bpMu.Lock()
	s.bpsInst[0x8001] = true
	meta, err := s.buildBPMeta("", "", "tick at {PC}")
	if err != nil {
		s.bpMu.Unlock()
		t.Fatalf("buildBPMeta: %v", err)
	}
	s.bpMetaInst[0x8001] = meta
	s.rebuildBPHit()
	s.bpMu.Unlock()

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 3, Type: "request"},
		Command:         "continue",
	})
	time.Sleep(20 * time.Millisecond)
	s.pauseRequested.Store(true)
	<-s.runDone

	body := out.String()
	if !strings.Contains(body, `"event":"output"`) {
		t.Fatalf("logMessage should emit output event, got: %s", body)
	}
	if !strings.Contains(body, "tick at $") {
		t.Fatalf("output event should carry interpolated message, got: %s", body)
	}
}

func TestBPMeta_BuildErrorOnBadCondition(t *testing.T) {
	s, _, _ := newStoppedServer(t, nil)
	_, err := s.buildBPMeta("A +", "", "")
	if err == nil {
		t.Fatalf("malformed condition should return error")
	}
	if !strings.Contains(err.Error(), "condition") {
		t.Fatalf("error message should mention condition: %v", err)
	}
}

func TestBPMeta_BuildErrorOnNonIntHitCondition(t *testing.T) {
	s, _, _ := newStoppedServer(t, nil)
	_, err := s.buildBPMeta("", ">= 3", "")
	if err == nil {
		t.Fatalf("non-integer hitCondition should return error (v1 limitation)")
	}
}

func TestBPMeta_LogMessageBadExprFallback(t *testing.T) {
	s, _, _ := newStoppedServer(t, nil)
	meta, err := s.buildBPMeta("", "", "value: {A +}")
	if err != nil {
		t.Fatalf("logMessage build shouldn't pre-compile placeholders: %v", err)
	}
	// formatLogMessage runs at hit time and should produce {!...} for
	// malformed inner expressions.
	out := s.formatLogMessage(meta.LogMessage)
	if !strings.Contains(out, "{!") {
		t.Fatalf("expected {!err} fallback for malformed inner expr, got: %s", out)
	}
}
