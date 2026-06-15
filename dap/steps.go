package dap

import (
	"fmt"
	"time"

	"github.com/nkane/chippy/cpu"
)

// chippyStateInterval throttles the custom `chippy-state` live-state event to
// at most ~60 Hz so a fast free-run can't flood the channel.
const chippyStateInterval = time.Second / 60

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
	s.installDirtyHook()
	defer func() {
		s.removeDirtyHook()
		s.running.Store(false)
		close(s.runDone)
	}()
	reason := "pause"
	var lastState time.Time
	for {
		if s.pauseRequested.Load() {
			reason = "pause"
			break
		}
		// Per-iteration lock so a concurrent TUI surface gets a window
		// to act between our steps. Holding the lock for the whole
		// loop would freeze the TUI for the duration of a continue.
		next, reasonAtBreak := s.runLoopIter()
		if next == "stop" {
			reason = reasonAtBreak
			break
		}
		// Stream live state to subscribers, throttled, so panels update
		// during the run without per-frame polling (issue #395).
		if time.Since(lastState) >= chippyStateInterval {
			s.sendChippyState()
			lastState = time.Now()
		}
	}
	s.sendEvent("stopped", StoppedEventBody{
		Reason:            reason,
		ThreadID:          1,
		AllThreadsStopped: true,
	})
}

// sendChippyState pushes a `chippy-state` event with the current register
// file plus the memory written since the previous event (issue #440). Reads
// regs and flushes the dirty spans under cpuMu so the snapshot can't tear
// across an instruction, then sends outside the lock.
func (s *Server) sendChippyState() {
	s.lockCPU()
	body := ChippyStateBody{
		A: s.cpu.A, X: s.cpu.X, Y: s.cpu.Y, SP: s.cpu.SP, P: s.cpu.P,
		PC: s.cpu.PC, Cycles: s.cpu.Cycles, Halted: s.cpu.Halted,
		DirtyRanges: s.flushDirtyRanges(),
	}
	s.unlockCPU()
	s.sendEvent(ChippyStateEvent, body)
}

// installDirtyHook arms the AccessWrite dirty-memory tracker for a free-run.
// Composes with any host-installed access hook (issue #433) by chaining, and
// resets the dirty bitmap so a prior run's tail (flushed only by the final
// stopped reconcile) doesn't leak into this run.
func (s *Server) installDirtyHook() {
	if s.dirty == nil {
		s.dirty = make([]bool, 0x10000)
	} else {
		clear(s.dirty)
	}
	s.dirtyLo, s.dirtyHi = 0x10000, -1
	s.prevAccessHook = s.cpu.AccessHook()
	prev := s.prevAccessHook
	s.cpu.SetAccessHook(func(addr uint16, kind cpu.AccessKind) {
		if prev != nil {
			prev(addr, kind)
		}
		if kind == cpu.AccessWrite {
			a := int(addr)
			s.dirty[a] = true
			if a < s.dirtyLo {
				s.dirtyLo = a
			}
			if a > s.dirtyHi {
				s.dirtyHi = a
			}
		}
	})
}

// removeDirtyHook restores the access hook that was installed before the run.
func (s *Server) removeDirtyHook() {
	s.cpu.SetAccessHook(s.prevAccessHook)
	s.prevAccessHook = nil
}

// flushDirtyRanges coalesces the dirty bitmap into half-open [Start, End)
// spans, snapshots each span's current bytes, clears the scanned bits, and
// resets the bounds. Must be called under cpuMu. Returns nil when nothing
// changed. The dirtyHi bound keeps the scan proportional to the touched
// region, not the full 64 KiB.
func (s *Server) flushDirtyRanges() []MemRange {
	if s.dirtyLo > s.dirtyHi {
		return nil
	}
	var ranges []MemRange
	i := s.dirtyLo
	for i <= s.dirtyHi {
		if !s.dirty[i] {
			i++
			continue
		}
		start := i
		for i <= s.dirtyHi && s.dirty[i] {
			s.dirty[i] = false
			i++
		}
		data := make([]byte, i-start)
		for j := start; j < i; j++ {
			data[j-start] = s.ram.Read(uint16(j))
		}
		ranges = append(ranges, MemRange{Start: uint16(start), End: uint16(i), Data: data})
	}
	s.dirtyLo, s.dirtyHi = 0x10000, -1
	return ranges
}

// runLoopIter runs one iteration of the continue loop under cpuMu.
// Returns "stop" + a reason when the loop should exit, "continue" + "" otherwise.
func (s *Server) runLoopIter() (string, string) {
	s.lockCPU()
	defer s.unlockCPU()
	// Exception filter: pause BEFORE executing a BRK so the user
	// can inspect state at the trap point rather than landing in
	// the IRQ handler. lastExceptionPC drives exceptionInfo's
	// description.
	if s.brkOnException.Load() && s.ram.Read(s.cpu.PC) == 0x00 {
		s.lastExceptionPC.Store(uint32(s.cpu.PC))
		return "stop", "exception"
	}
	s.stepWithSnapshot(func() { s.cpu.Step() })
	if s.cpu.Halted {
		return "stop", "exception"
	}
	// Host stop condition (issue #433): NES step granularity (run-to-NMI,
	// step-scanline, …) the host expresses without a side-loop. Checked
	// post-step under cpuMu so the host sees the just-advanced state.
	if s.stopPredicate != nil && s.stopPredicate() {
		return "stop", "step"
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
			return "stop", "breakpoint"
		}
		// Continue past a non-firing bp: step once so we don't
		// re-trigger on the same PC immediately.
		s.stepWithSnapshot(func() { s.cpu.Step() })
		if s.cpu.Halted {
			return "stop", "exception"
		}
	}
	return "continue", ""
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
// SP is 8-bit and wraps from $FF to $00 on push, $00 to $FF on pop. A
// naive `SP > startSP` comparison breaks when an RTS lifts SP through
// the boundary ($FE → $00, say). Treating the modular delta as a signed
// 8-bit value gives a "did we rise?" test that handles the wrap.
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
			if int8(s.cpu.SP-startSP) > 0 {
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
