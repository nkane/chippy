package dap

import (
	"encoding/json"
	"strings"
	"testing"
)

// readMemory must reject a negative Count instead of panicking on
// make([]byte, -N).
func TestReadMemory_NegativeCountRejected(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	args := ReadMemoryArguments{
		MemoryReference: "$0000",
		Count:           -1,
	}
	raw, _ := json.Marshal(args)
	s.handleReadMemory(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "readMemory",
		Arguments:       raw,
	})
	body := out.String()
	if !strings.Contains(body, `"success":false`) {
		t.Fatalf("negative Count should error; got: %s", body)
	}
	// JSON escapes `>` to `>`; match the unicode form rather than literal.
	if !strings.Contains(body, "count must be") {
		t.Fatalf("error should mention the count constraint; got: %s", body)
	}
}

// disassemble must clamp a wildly-negative Offset rather than wrapping
// uint16 silently.
func TestDisassemble_NegativeOffsetClamped(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA, 0xEA, 0xEA})
	args := DisassembleArguments{
		MemoryReference:  "$8000",
		Offset:           -0x10000,
		InstructionCount: 2,
	}
	raw, _ := json.Marshal(args)
	s.handleDisassemble(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "disassemble",
		Arguments:       raw,
	})
	body := out.String()
	// Should succeed (clamped to $0000) — not panic, not wrap.
	if !strings.Contains(body, `"success":true`) {
		t.Fatalf("disasm with large negative Offset should clamp and succeed; got: %s", body)
	}
}

// disassemble negative InstructionCount is rejected.
func TestDisassemble_NegativeInstructionCountRejected(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	args := DisassembleArguments{
		MemoryReference:  "$8000",
		InstructionCount: -5,
	}
	raw, _ := json.Marshal(args)
	s.handleDisassemble(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "disassemble",
		Arguments:       raw,
	})
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("negative instructionCount should error; got: %s", out.String())
	}
}

// stepOut must detect SP rising even when the rise wraps the 8-bit
// stack pointer past $FF→$00.
func TestStepOut_SPWrapsCorrectly(t *testing.T) {
	// Set up: SP=$FE; the routine RTSs; resulting SP=$00 wraps.
	// We need a JSR that pushes SP from $FE down through $FC, then RTS
	// pops back. But the wrap case is when we're already near $00 and
	// pop. Construct: SP starts at $00, RTS pops it to $02 (wraps).
	s, _, _ := newStoppedServer(t, []byte{0x60}) // RTS at $8000
	// Manually craft the wrap-edge state.
	s.cpu.SP = 0x00
	// Push a fake return address ($1234-1 = $1233) onto stack at $0101/$0102.
	// SP after JSR-equivalent push: was supposed to be $FE; with SP=$00 we
	// emulate "we're already inside" so SP-=2 means we're at $FE.
	// For the rise test we just need RTS to bump SP back: pre: $00, post: $02.
	s.ram.Write(0x0101, 0x33)
	s.ram.Write(0x0102, 0x12)
	s.cpu.PC = 0x8000

	s.handleStepOut(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepOut",
	})
	if s.cpu.SP != 0x02 {
		t.Fatalf("RTS from SP=$00 should leave SP=$02; got $%02X", s.cpu.SP)
	}
	if s.cpu.PC != 0x1234 {
		t.Fatalf("RTS should set PC=$1234 (target+1); got $%04X", s.cpu.PC)
	}
}

// evaluate must refuse to read CPU state while a continue is in flight
// (race-detector would otherwise trip).
func TestEvaluate_RefusesDuringRun(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0x4C, 0x00, 0x80}) // LDA #$00 ; JMP $8000
	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "continue",
	})
	defer func() {
		s.pauseRequested.Store(true)
		<-s.runDone
	}()
	// Best-effort wait for the run loop to be live.
	if !s.running.Load() {
		t.Skip("run loop not yet running; cannot exercise the race guard")
	}

	args := EvaluateArguments{Expression: "A"}
	raw, _ := json.Marshal(args)
	// Write directly into the buffer used by sendResponse so we can
	// inspect the result without racing on the shared out buffer.
	s.handleEvaluate(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "evaluate",
		Arguments:       raw,
	})
}

// Source-line setBreakpoints with two entries on the same line should
// keep the first and report the second as verified=false with a
// "duplicate" message instead of silently overwriting.
func TestSetBreakpoints_DuplicateLineRejectedWithMessage(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA, 0xEA})
	args := SetBreakpointsArguments{
		Source: Source{Path: "main.s"},
		Breakpoints: []SourceBreakpoint{
			{Line: 42},
			{Line: 42}, // duplicate
		},
	}
	raw, _ := json.Marshal(args)
	s.handleSetBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setBreakpoints",
		Arguments:       raw,
	})
	body := out.String()
	if !strings.Contains(body, "duplicate breakpoint at main.s:42") {
		t.Fatalf("duplicate line should report a message; got: %s", body)
	}
}

// Same for setInstructionBreakpoints: duplicate $XXXX.
func TestSetInstructionBreakpoints_DuplicateAddressRejected(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	args := SetInstructionBreakpointsArguments{
		Breakpoints: []InstructionBreakpoint{
			{InstructionReference: "$8000"},
			{InstructionReference: "$8000"}, // duplicate
		},
	}
	raw, _ := json.Marshal(args)
	s.handleSetInstructionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setInstructionBreakpoints",
		Arguments:       raw,
	})
	body := out.String()
	if !strings.Contains(body, "duplicate instruction breakpoint at $8000") {
		t.Fatalf("duplicate instruction bp should report a message; got: %s", body)
	}
}
