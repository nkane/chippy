package dap

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

// runLoop free-runs the CPU until pauseRequested is set or the CPU halts.
// Emits the `stopped` event before signaling runDone. Single goroutine —
// safe to mutate cpu state without locks because no other handler runs
// while running=true (single-step handlers refuse, and disconnect waits
// for runDone).
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
		s.cpu.Step()
		if s.cpu.Halted {
			reason = "exception"
			break
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
	s.cpu.Step()
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
	if op != 0x20 {
		s.cpu.Step()
	} else {
		retPC := s.cpu.PC + 3
		for i := 0; i < guardSteps; i++ {
			s.cpu.Step()
			if s.cpu.Halted || s.cpu.PC == retPC {
				break
			}
		}
	}
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
	for i := 0; i < guardSteps; i++ {
		s.cpu.Step()
		if s.cpu.Halted {
			break
		}
		if s.cpu.SP > startSP {
			break
		}
	}
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
