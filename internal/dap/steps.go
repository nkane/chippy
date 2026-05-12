package dap

import (
	"fmt"

	"github.com/nkane/chippy/internal/cpu"
)

// Step controls — continue / next / stepIn / stepOut / pause — plus the
// thin `threads` handler that 6502 needs (always one virtual thread).
//
// Free-run vs. step:
//   - `continue` spawns a goroutine that calls cpu.Step in a tight loop
//     until pauseRequested flips true or the CPU halts. The handler
//     responds immediately; the goroutine emits the `stopped` event when
//     it exits.
//   - Single-step requests (next/stepIn/stepOut) refuse if a continue is
//     in flight. They execute synchronously and emit `stopped` right
//     after responding.
//   - `pause` flips pauseRequested; the run goroutine notices on its
//     next loop iteration. The stopped event comes from the goroutine,
//     not the pause handler.

// guardSteps caps stepOut / next loops at this many instructions so a
// runaway program doesn't pin the goroutine.
const guardSteps = 2_000_000

// outputEventBody mirrors the DAP `output` event payload. Used by
// logpoint emissions and any future server-to-console traffic.
type outputEventBody struct {
	Category string `json:"category,omitempty"`
	Output   string `json:"output"`
}

func (s *Server) handleThreads(req Request) {
	type thread struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	type body struct {
		Threads []thread `json:"threads"`
	}
	s.sendResponse(req, body{
		Threads: []thread{{ID: 1, Name: "cpu"}},
	})
}

func (s *Server) handleContinue(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	if s.running.Load() {
		s.sendErrorResponse(req, "already running")
		return
	}
	s.pauseRequested.Store(false)
	s.runDone = make(chan struct{})
	s.running.Store(true)

	type body struct {
		AllThreadsContinued bool `json:"allThreadsContinued"`
	}
	s.sendResponse(req, body{AllThreadsContinued: true})

	go s.runLoop()
}

// runLoop free-runs the CPU until pauseRequested is set, the CPU halts,
// or PC lands on a breakpoint. Emits the `stopped` event before
// signaling runDone. Single goroutine — safe to mutate cpu state
// without locks because no other handler runs while running=true
// (single-step handlers refuse, and disconnect waits for runDone).
// Breakpoint reads are guarded by bpMu inside isBreakpoint so concurrent
// setBreakpoints requests stay safe.
func (s *Server) runLoop() {
	defer func() {
		s.running.Store(false)
		close(s.runDone)
	}()
	reason := "pause"
	for {
		if s.pauseRequested.Load() {
			reason = "pause"
			break
		}
		// Exception filter: pause BEFORE executing a BRK so the user
		// can inspect state at the trap point rather than landing in
		// the IRQ handler. lastExceptionPC drives exceptionInfo's
		// description.
		if s.brkOnException.Load() && s.ram.Read(s.cpu.PC) == 0x00 {
			s.lastExceptionPC.Store(uint32(s.cpu.PC))
			reason = "exception"
			break
		}
		s.stepWithSnapshot(func() { s.cpu.Step() })
		if s.cpu.Halted {
			reason = "exception"
			break
		}
		if s.isBreakpoint(s.cpu.PC) {
			meta := s.lookupBPMeta(s.cpu.PC)
			fire, logLine := s.shouldFireBP(meta)
			if logLine != "" {
				s.sendEvent("output", outputEventBody{
					Category: "console",
					Output:   logLine + "\n",
				})
			}
			if fire {
				reason = "breakpoint"
				break
			}
			// Continue past a non-firing bp: step once so we don't
			// re-trigger on the same PC immediately.
			s.stepWithSnapshot(func() { s.cpu.Step() })
			if s.cpu.Halted {
				reason = "exception"
				break
			}
		}
	}
	s.sendEvent("stopped", StoppedEventBody{
		Reason:            reason,
		ThreadID:          1,
		AllThreadsStopped: true,
	})
}

func (s *Server) handlePause(req Request) {
	if !s.running.Load() {
		s.sendErrorResponse(req, "not running")
		return
	}
	s.pauseRequested.Store(true)
	s.sendResponse(req, nil)
}

func (s *Server) handleStepIn(req Request) {
	if !s.requireStopped(req) {
		return
	}
	s.stepWithSnapshot(func() { s.cpu.Step() })
	s.sendResponse(req, nil)
	s.sendEvent("stopped", StoppedEventBody{Reason: "step", ThreadID: 1, AllThreadsStopped: true})
}

// stepWithSnapshot wraps one or more cpu.Step calls in a single rewind
// snapshot. Captures regs + peripherals BEFORE the step, resets the
// RAM shadow epoch, runs the step body, and folds the page deltas into
// the pushed snapshot. Issue #66: CoW deltas keep a wide sweep
// (next-over-JSR, stepOut) at the same memory cost as a single Step.
func (s *Server) stepWithSnapshot(body func()) {
	if s.rewind == nil || s.cpu == nil || s.ram == nil {
		body()
		return
	}
	snap := s.cpu.Snapshot(s.ram)
	s.capturePeripherals(&snap)
	s.ram.ResetShadow()
	body()
	snap.Pages = s.ram.TakeShadow()
	s.rewind.Push(snap)
}

func (s *Server) capturePeripherals(snap *cpu.Snapshot) {
	if s.textOut == nil && s.keyIn == nil {
		return
	}
	snap.Peripherals = map[string][]byte{}
	if s.textOut != nil {
		snap.Peripherals[fmt.Sprintf("$%04X", s.textOut.Addr)] = s.textOut.Snapshot()
	}
	if s.keyIn != nil {
		snap.Peripherals[fmt.Sprintf("$%04X", s.keyIn.DataAddr)] = s.keyIn.Snapshot()
	}
}

func (s *Server) restorePeripherals(snap cpu.Snapshot) {
	if snap.Peripherals == nil {
		return
	}
	if s.textOut != nil {
		if state, ok := snap.Peripherals[fmt.Sprintf("$%04X", s.textOut.Addr)]; ok {
			s.textOut.Restore(state)
		}
	}
	if s.keyIn != nil {
		if state, ok := snap.Peripherals[fmt.Sprintf("$%04X", s.keyIn.DataAddr)]; ok {
			s.keyIn.Restore(state)
		}
	}
}

// handleStepBack pops one snapshot and restores. Same protocol shape as
// stepIn — request response + stopped event. Reason="step" because DAP
// has no reverse-specific reason and editors render the stop the same
// way either direction.
func (s *Server) handleStepBack(req Request) {
	if !s.requireStopped(req) {
		return
	}
	if s.rewind == nil || s.rewind.Len() == 0 {
		s.sendErrorResponse(req, "rewind buffer is empty")
		return
	}
	snap, _ := s.rewind.Pop()
	s.cpu.Restore(snap, s.ram)
	s.restorePeripherals(snap)
	s.sendResponse(req, nil)
	s.sendEvent("stopped", StoppedEventBody{Reason: "step", ThreadID: 1, AllThreadsStopped: true})
}

// handleNext = step-over. If the current opcode is a JSR ($20), run until
// PC reaches the address just past the call. Otherwise single-step.
func (s *Server) handleNext(req Request) {
	if !s.requireStopped(req) {
		return
	}
	op := s.ram.Read(s.cpu.PC)
	s.stepWithSnapshot(func() {
		if op != 0x20 {
			s.cpu.Step()
			return
		}
		retPC := s.cpu.PC + 3
		for i := 0; i < guardSteps; i++ {
			s.cpu.Step()
			if s.cpu.Halted || s.cpu.PC == retPC {
				break
			}
		}
	})
	s.sendResponse(req, nil)
	s.sendEvent("stopped", StoppedEventBody{Reason: "step", ThreadID: 1, AllThreadsStopped: true})
}

// handleStepOut runs until SP rises above the current frame's SP — i.e.
// the current routine has popped its return address and RTS'd back.
func (s *Server) handleStepOut(req Request) {
	if !s.requireStopped(req) {
		return
	}
	startSP := s.cpu.SP
	s.stepWithSnapshot(func() {
		for i := 0; i < guardSteps; i++ {
			s.cpu.Step()
			if s.cpu.Halted {
				break
			}
			if s.cpu.SP > startSP {
				break
			}
		}
	})
	s.sendResponse(req, nil)
	s.sendEvent("stopped", StoppedEventBody{Reason: "step", ThreadID: 1, AllThreadsStopped: true})
}

// requireStopped checks that a debuggee exists and isn't in continue mode.
// Sends an error response when either check fails and returns false.
func (s *Server) requireStopped(req Request) bool {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return false
	}
	if s.running.Load() {
		s.sendErrorResponse(req, "CPU is running; pause first")
		return false
	}
	return true
}
