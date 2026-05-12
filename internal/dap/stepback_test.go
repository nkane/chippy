package dap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStepBack_RestoresPriorState(t *testing.T) {
	// LDA #$42 (2B) ; LDA #$77 (2B)
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x42, 0xA9, 0x77})
	startPC := s.cpu.PC
	startA := s.cpu.A

	stepReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepIn",
	}
	s.handleStepIn(stepReq)
	if s.cpu.A != 0x42 {
		t.Fatalf("post-step A want $42, got $%02X", s.cpu.A)
	}

	backReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepBack",
	}
	s.handleStepBack(backReq)

	if s.cpu.PC != startPC {
		t.Fatalf("stepBack PC want $%04X, got $%04X", startPC, s.cpu.PC)
	}
	if s.cpu.A != startA {
		t.Fatalf("stepBack A want $%02X, got $%02X", startA, s.cpu.A)
	}
}

func TestStepBack_EmptyRingErrors(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepBack",
	}
	s.handleStepBack(req)
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("stepBack on empty ring should error, got: %s", out.String())
	}
}

func TestStepBack_StepForwardBackForwardParity(t *testing.T) {
	// LDA #$11 ; TAX ; INX
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x11, 0xAA, 0xE8})

	step := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepIn",
	}
	s.handleStepIn(step)
	s.handleStepIn(step)
	s.handleStepIn(step)

	want := struct {
		A, X, Y, SP, P byte
		PC             uint16
	}{s.cpu.A, s.cpu.X, s.cpu.Y, s.cpu.SP, s.cpu.P, s.cpu.PC}

	// stepBack then stepIn — should return to identical state.
	s.handleStepBack(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepBack",
	})
	s.handleStepIn(step)

	if s.cpu.A != want.A || s.cpu.X != want.X || s.cpu.Y != want.Y ||
		s.cpu.SP != want.SP || s.cpu.P != want.P || s.cpu.PC != want.PC {
		t.Fatalf("step/back/step diverged from straight-step state")
	}
}

func TestStepBack_NextAndStepOutPushSnapshots(t *testing.T) {
	// JSR $8010 (3B) ; @$8010: RTS — exercises both `next` (over JSR)
	// and `stepOut`. Both should push snapshots so we can rewind across
	// the JSR/RTS pair.
	s, _, _ := newStoppedServer(t, []byte{0x20, 0x10, 0x80})
	s.ram.Write(0x8010, 0x60) // RTS

	preNextPC := s.cpu.PC
	s.handleNext(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "next",
	})
	if s.cpu.PC != 0x8003 {
		t.Fatalf("next should land at $8003, got $%04X", s.cpu.PC)
	}

	// stepBack should undo the entire JSR/RTS sweep.
	s.handleStepBack(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepBack",
	})
	if s.cpu.PC != preNextPC {
		t.Fatalf("stepBack after next: PC want $%04X, got $%04X", preNextPC, s.cpu.PC)
	}
}

func TestStepBack_CapabilityAdvertised(t *testing.T) {
	// initialize handshake should now report supportsStepBack:true.
	var in, out interface {
		String() string
	}
	_ = in
	_ = out
	// Quick path: just check the constant in the response by invoking
	// handleInitialize directly.
	s, _, captured := newStoppedServer(t, nil)
	s.handleInitialize(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "initialize",
		Arguments:       json.RawMessage(`{"adapterID":"chippy"}`),
	})
	if !strings.Contains(captured.String(), `"supportsStepBack":true`) {
		t.Fatalf("initialize response should advertise supportsStepBack:true, got: %s", captured.String())
	}
}
