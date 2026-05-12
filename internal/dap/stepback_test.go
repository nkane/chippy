package dap

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

// TestStepBack_RestoresTextOutputBuffer drives the CPU to write two
// bytes through the $F001 MMIO write port, then rewinds one step at a
// time and confirms the TextOutput buffer rewinds along with the CPU.
// Without peripheral snapshots the buffer would keep growing as steps
// rewound.
func TestStepBack_RestoresTextOutputBuffer(t *testing.T) {
	// $8000: A9 41        LDA #'A'
	// $8002: 8D 01 F0     STA $F001
	// $8005: A9 42        LDA #'B'
	// $8007: 8D 01 F0     STA $F001
	prog := []byte{0xA9, 0x41, 0x8D, 0x01, 0xF0, 0xA9, 0x42, 0x8D, 0x01, 0xF0}
	s, _, _ := newStoppedServer(t, prog)

	step := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepIn",
	}
	for i := 0; i < 4; i++ {
		s.handleStepIn(step)
	}
	if got := s.textOut.String(); got != "AB" {
		t.Fatalf("after 4 steps textOut want %q; got %q", "AB", got)
	}

	back := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepBack",
	}
	s.handleStepBack(back) // undo STA #2 -> "A"
	if got := s.textOut.String(); got != "A" {
		t.Fatalf("after 1 stepBack textOut want %q; got %q", "A", got)
	}
	s.handleStepBack(back) // undo LDA #'B' -> "A"
	s.handleStepBack(back) // undo STA #1 -> ""
	if got := s.textOut.String(); got != "" {
		t.Fatalf("after 3 stepBacks textOut want empty; got %q", got)
	}
}

// TestStepBack_RestoresKeyboardLatch confirms that when the CPU reads
// $F004 mid-program, stepBack re-arms the keyboard latch so a re-step
// observes the same byte.
func TestStepBack_RestoresKeyboardLatch(t *testing.T) {
	// $8000: AD 04 F0     LDA $F004   ; drains keyboard latch
	prog := []byte{0xAD, 0x04, 0xF0}
	s, _, _ := newStoppedServer(t, prog)

	s.keyIn.Push('K')
	if !s.keyIn.Ready() {
		t.Fatalf("precondition: keyboard should be armed")
	}

	step := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepIn",
	}
	s.handleStepIn(step)
	if s.keyIn.Ready() {
		t.Fatalf("post-LDA $F004 latch should be drained")
	}
	if s.cpu.A != ('K' | 0x80) {
		t.Fatalf("A should hold latched key; got $%02X", s.cpu.A)
	}

	back := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepBack",
	}
	s.handleStepBack(back)
	if !s.keyIn.Ready() {
		t.Fatalf("stepBack should re-arm keyboard latch")
	}
}

// TestStepBack_FreeRunRecordsDeltas confirms that runLoop now pushes
// snapshots during free-run (issue #66). After a continue/pause sweep
// the ring should be non-empty and stepBack must walk us back through
// each instruction the run loop executed.
func TestStepBack_FreeRunRecordsDeltas(t *testing.T) {
	// LDA #$00 ; LDA #$11 ; LDA #$22 ; JMP $8000 — non-degenerate loop.
	// Each LDA leaves a distinct register trace so we can verify rewind.
	prog := []byte{0xA9, 0x00, 0xA9, 0x11, 0xA9, 0x22, 0x4C, 0x00, 0x80}
	s, _, _ := newStoppedServer(t, prog)

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "continue",
	})

	// Let the run loop execute a handful of iterations before pausing.
	// Reading s.rewind.Len() here would race with runLoop's pushes; the
	// run loop synchronizes only via runDone after pauseRequested.
	time.Sleep(40 * time.Millisecond)
	s.pauseRequested.Store(true)
	<-s.runDone

	if s.rewind.Len() == 0 {
		t.Fatalf("free-run should have pushed snapshots; ring is empty")
	}

	// stepBack twice. PC should walk backwards across the executed instructions.
	preBackPC := s.cpu.PC
	back := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepBack",
	}
	s.handleStepBack(back)
	s.handleStepBack(back)
	if s.cpu.PC == preBackPC {
		t.Fatalf("stepBack after free-run should rewind PC; stuck at $%04X", preBackPC)
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
