package dap

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/peripheral"
)

// newCMOSCPUForTest returns a CMOS-variant CPU wired to ram. Used by
// memory_test.go to confirm variant-aware disassembly through DAP.
func newCMOSCPUForTest(t *testing.T, ram *cpu.RAM) *cpu.CPU {
	t.Helper()
	return cpu.NewVariant(ram, cpu.VariantCMOS65C02)
}

// newStoppedServer constructs a Server with a debuggee pre-wired to
// match what bootDebuggee builds, then returns it ready for step
// requests. Skipping the launch round-trip lets tests focus on the
// step semantics. The caller supplies the program bytes; entry point
// is $8000 with reset vector pointing at it.
func newStoppedServer(t *testing.T, prog []byte) (*Server, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	ram := cpu.NewRAM()
	ram.Load(0x8000, prog)
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	mmio := cpu.NewMMIO(ram)
	textOut := peripheral.NewTextOutput(0xF001)
	keyIn := peripheral.NewKeyboardInput(0xF004, 0xF005)
	_ = mmio.Register(textOut)
	_ = mmio.Register(keyIn)
	c := cpu.New(mmio)

	var in, out bytes.Buffer
	s := NewServer(&in, &out)
	s.cpu = c
	s.ram = ram
	s.mmio = mmio
	s.textOut = textOut
	s.keyIn = keyIn
	return s, &in, &out
}

func TestSteps_StepInAdvancesOneInstruction(t *testing.T) {
	// LDA #$42 (2 bytes) ; LDA #$77 (2 bytes)
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x42, 0xA9, 0x77})
	startPC := s.cpu.PC

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepIn",
	}
	s.handleStepIn(req)

	if s.cpu.PC != startPC+2 {
		t.Fatalf("stepIn should advance one instruction; PC went $%04X -> $%04X", startPC, s.cpu.PC)
	}
	if s.cpu.A != 0x42 {
		t.Fatalf("LDA #$42 should set A=$42, got $%02X", s.cpu.A)
	}
}

func TestSteps_NextOverJSRRunsToReturn(t *testing.T) {
	// JSR $8010 ; NOP ; ... ; @$8010: RTS
	// Layout: $8000 JSR $8010 (3 bytes) -> next-pc = $8003
	//         $8003 EA NOP
	//         $8010 60 RTS
	s, _, _ := newStoppedServer(t, []byte{0x20, 0x10, 0x80, 0xEA})
	s.ram.Write(0x8010, 0x60) // RTS

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "next",
	}
	s.handleNext(req)

	if s.cpu.PC != 0x8003 {
		t.Fatalf("next over JSR should land at $8003 (post-call), got $%04X", s.cpu.PC)
	}
}

func TestSteps_NextOverNonJSRSingleSteps(t *testing.T) {
	// Two NOPs.
	s, _, _ := newStoppedServer(t, []byte{0xEA, 0xEA})
	startPC := s.cpu.PC

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "next",
	}
	s.handleNext(req)

	if s.cpu.PC != startPC+1 {
		t.Fatalf("next over NOP should single-step, got PC $%04X (started $%04X)", s.cpu.PC, startPC)
	}
}

func TestSteps_StepOutRunsUntilSPRises(t *testing.T) {
	// JSR $8010 ; @$8010: RTS
	s, _, _ := newStoppedServer(t, []byte{0x20, 0x10, 0x80})
	s.ram.Write(0x8010, 0x60) // RTS

	// First step the JSR so we're inside the routine.
	stepReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stepIn",
	}
	s.handleStepIn(stepReq)
	if s.cpu.PC != 0x8010 {
		t.Fatalf("setup: stepIn should land in callee at $8010, got $%04X", s.cpu.PC)
	}

	// Now stepOut should run the RTS and return us past the JSR.
	outReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepOut",
	}
	s.handleStepOut(outReq)

	if s.cpu.PC != 0x8003 {
		t.Fatalf("stepOut should return to $8003, got $%04X", s.cpu.PC)
	}
}

func TestSteps_ContinuePauseRoundTrip(t *testing.T) {
	// Tight loop: LDA #$00 (2 bytes) ; JMP $8000 — runs forever until
	// pause. A bare `JMP $8000` at $8000 would trip Step()'s self-jump
	// halt detection (PC == startPC -> Halted=true).
	s, _, out := newStoppedServer(t, []byte{0xA9, 0x00, 0x4C, 0x00, 0x80})

	contReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "continue",
	}
	s.handleContinue(contReq)
	if !s.running.Load() {
		t.Fatalf("continue should mark server as running")
	}
	// Let the run loop spin briefly so we know it's actually executing.
	time.Sleep(20 * time.Millisecond)

	pauseReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "pause",
	}
	s.handlePause(pauseReq)

	// Wait for the run loop to observe pauseRequested and shut down.
	<-s.runDone
	if s.running.Load() {
		t.Fatalf("running should be false after run loop exits")
	}

	// Two responses (continue, pause) + one stopped event should be in
	// the output buffer.
	got := out.String()
	for _, want := range []string{
		`"command":"continue"`,
		`"command":"pause"`,
		`"event":"stopped"`,
		`"reason":"pause"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}

func TestSteps_PauseWhenNotRunningErrors(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "pause",
	}
	s.handlePause(req)

	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("pause-when-not-running should produce error response")
	}
}

func TestSteps_StepInWhileRunningErrors(t *testing.T) {
	// LDA #$00 ; JMP $8000 — non-degenerate infinite loop.
	s, _, out := newStoppedServer(t, []byte{0xA9, 0x00, 0x4C, 0x00, 0x80})

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "continue",
	})

	// While running=true, stepIn must refuse.
	s.handleStepIn(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "stepIn",
	})

	// Stop the run loop before reading out — avoids racing with the
	// stopped event the runLoop emits when it exits.
	s.pauseRequested.Store(true)
	<-s.runDone

	if !strings.Contains(out.String(), `"command":"stepIn"`) ||
		!strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("stepIn while running should produce error response, got:\n%s", out.String())
	}
}

func TestSteps_ThreadsReturnsSingleVirtualThread(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.handleThreads(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "threads",
	})
	body := out.String()
	if !strings.Contains(body, `"id":1`) || !strings.Contains(body, `"name":"cpu"`) {
		t.Fatalf("threads response should list a single cpu thread, got: %s", body)
	}
}
