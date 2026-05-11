package dap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/symbols"
)

func TestBP_SetInstructionBreakpoints(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA, 0xEA, 0xEA})

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"instructionReference":"$8002"}]}`),
	}
	s.handleSetInstructionBreakpoints(req)

	if !strings.Contains(out.String(), `"verified":true`) {
		t.Fatalf("expected verified=true, got: %s", out.String())
	}
	if !s.isBreakpoint(0x8002) {
		t.Fatalf("isBreakpoint($8002) should be true after set")
	}
}

func TestBP_SetInstructionBreakpointsReplacesAll(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xEA})
	first := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"instructionReference":"$8002"},{"instructionReference":"$8004"}]}`),
	}
	s.handleSetInstructionBreakpoints(first)
	if !s.isBreakpoint(0x8002) || !s.isBreakpoint(0x8004) {
		t.Fatalf("first set should install both bps")
	}
	second := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"instructionReference":"$9000"}]}`),
	}
	s.handleSetInstructionBreakpoints(second)
	if s.isBreakpoint(0x8002) || s.isBreakpoint(0x8004) {
		t.Fatalf("second set should clear prior instruction bps")
	}
	if !s.isBreakpoint(0x9000) {
		t.Fatalf("second set should install $9000")
	}
}

func TestBP_RunStopsOnBreakpoint(t *testing.T) {
	// LDA #$00 ; LDA #$11 ; JMP $8000 — non-degenerate loop.
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0xA9, 0x11, 0x4C, 0x00, 0x80})
	// Breakpoint at $8002 (the second LDA).
	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"instructionReference":"$8002"}]}`),
	})

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})

	<-s.runDone
	if s.cpu.PC != 0x8002 {
		t.Fatalf("run should stop with PC=$8002, got $%04X", s.cpu.PC)
	}
}

func TestBP_SourceLineResolvesViaSrcMap(t *testing.T) {
	// Build a tiny fake source map: $8002 -> main.s:5.
	s, _, out := newStoppedServer(t, []byte{0xEA, 0xEA, 0xEA})
	s.srcMap = &symbols.SourceMap{
		PCToSrc: map[uint16]symbols.SrcLoc{
			0x8002: {File: "main.s", Line: 5},
		},
		Files: map[string][]string{"main.s": {"line1", "line2", "line3", "line4", "line5"}},
	}

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setBreakpoints",
		Arguments: json.RawMessage(`{
			"source":{"name":"main.s","path":"main.s"},
			"breakpoints":[{"line":5}]
		}`),
	}
	s.handleSetBreakpoints(req)

	body := out.String()
	if !strings.Contains(body, `"verified":true`) {
		t.Fatalf("expected verified=true, got: %s", body)
	}
	if !strings.Contains(body, `"$8002"`) {
		t.Fatalf("expected resolved IP=$8002, got: %s", body)
	}
	if !s.isBreakpoint(0x8002) {
		t.Fatalf("bp set should activate $8002")
	}
}

func TestBP_SourceLineUnresolved(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.srcMap = &symbols.SourceMap{
		PCToSrc: map[uint16]symbols.SrcLoc{},
	}
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setBreakpoints",
		Arguments: json.RawMessage(`{
			"source":{"name":"main.s","path":"main.s"},
			"breakpoints":[{"line":99}]
		}`),
	}
	s.handleSetBreakpoints(req)
	if !strings.Contains(out.String(), `"verified":false`) {
		t.Fatalf("unresolved bp should report verified=false, got: %s", out.String())
	}
}

func TestBP_SetBreakpointsReplacesAllForSource(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xEA})
	s.srcMap = &symbols.SourceMap{
		PCToSrc: map[uint16]symbols.SrcLoc{
			0x8001: {File: "a.s", Line: 1},
			0x8002: {File: "a.s", Line: 2},
			0x8003: {File: "b.s", Line: 1},
		},
	}

	// Two bps in a.s.
	s.handleSetBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setBreakpoints",
		Arguments: json.RawMessage(`{
			"source":{"name":"a.s","path":"a.s"},
			"breakpoints":[{"line":1},{"line":2}]
		}`),
	})
	// One bp in b.s.
	s.handleSetBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setBreakpoints",
		Arguments: json.RawMessage(`{
			"source":{"name":"b.s","path":"b.s"},
			"breakpoints":[{"line":1}]
		}`),
	})
	if !s.isBreakpoint(0x8001) || !s.isBreakpoint(0x8002) || !s.isBreakpoint(0x8003) {
		t.Fatalf("after independent sources are set, all bps should be live")
	}

	// Replace a.s with a single bp at line 2 only — a.s:1 should drop.
	s.handleSetBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 3, Type: "request"},
		Command:         "setBreakpoints",
		Arguments: json.RawMessage(`{
			"source":{"name":"a.s","path":"a.s"},
			"breakpoints":[{"line":2}]
		}`),
	})
	if s.isBreakpoint(0x8001) {
		t.Fatalf("a.s:1 should have been cleared")
	}
	if !s.isBreakpoint(0x8002) {
		t.Fatalf("a.s:2 should still be set")
	}
	if !s.isBreakpoint(0x8003) {
		t.Fatalf("b.s bp should be unaffected by a.s rewrite")
	}

	// Empty breakpoints array for a.s -> all a.s bps drop.
	s.handleSetBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 4, Type: "request"},
		Command:         "setBreakpoints",
		Arguments: json.RawMessage(`{
			"source":{"name":"a.s","path":"a.s"},
			"breakpoints":[]
		}`),
	})
	if s.isBreakpoint(0x8002) {
		t.Fatalf("empty a.s set should clear $8002")
	}
	if !s.isBreakpoint(0x8003) {
		t.Fatalf("b.s bp should still be set")
	}
}
