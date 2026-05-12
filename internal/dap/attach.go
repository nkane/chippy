package dap

import (
	"encoding/json"
	"fmt"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/peripheral"
	"github.com/nkane/chippy/internal/symbols"
)

// AttachConfig is the externally-supplied debuggee a hosting process
// hands to the DAP server before/while the editor connects. The hosting
// process (e.g. the TUI when it later grows a `:dap` command) owns the
// CPU + RAM + peripherals; the DAP server just observes and steps them
// when the editor asks.
//
// Compare LaunchArguments, which builds the debuggee from scratch inside
// the server. Attach is the path for sharing an already-running CPU.
type AttachConfig struct {
	CPU     *cpu.CPU
	RAM     *cpu.RAM
	MMIO    *cpu.MMIO
	Tracer  *cpu.FileTracer
	Syms    *symbols.Table
	SrcMap  *symbols.SourceMap
	TextOut *peripheral.TextOutput
	KeyIn   *peripheral.KeyboardInput
}

// AttachExisting wires an already-constructed debuggee into the server
// without going through the loader. The hosting process calls this
// before Serve() so the next attach request finds the state ready.
// Returns an error if a debuggee is already wired.
func (s *Server) AttachExisting(cfg AttachConfig) error {
	if s.cpu != nil {
		return fmt.Errorf("a debuggee is already attached; disconnect first")
	}
	if cfg.CPU == nil || cfg.RAM == nil {
		return fmt.Errorf("AttachConfig requires at minimum a CPU and RAM")
	}
	s.cpu = cfg.CPU
	s.ram = cfg.RAM
	s.ram.EnableShadow() // CoW page tracking powers stepBack (issue #66).
	s.mmio = cfg.MMIO
	s.tracer = cfg.Tracer
	s.syms = cfg.Syms
	s.srcMap = cfg.SrcMap
	s.textOut = cfg.TextOut
	s.keyIn = cfg.KeyIn
	return nil
}

// handleAttach acknowledges an editor's attach request. v1 supports
// only the "attach to the debuggee already wired by AttachExisting"
// flow. Cross-process attach (a separate chippy connecting to a
// long-running session over a side channel) is a follow-up.
//
// stopOnEntry: when true (default), emit `stopped` with reason=entry so
// the editor immediately renders state. When false, the run loop the
// hosting process drives keeps going — the editor sees state at the
// next natural stop (breakpoint, pause, exception).
func (s *Server) handleAttach(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee wired; the host process must call AttachExisting before serving")
		return
	}
	var args AttachArguments
	if len(req.Arguments) > 0 {
		if err := json.Unmarshal(req.Arguments, &args); err != nil {
			s.sendErrorResponse(req, fmt.Sprintf("bad attach args: %v", err))
			return
		}
	}
	s.sendResponse(req, nil)
	// StopOnEntry: pointer-valued, three states.
	//   nil  → default: emit stopped(entry) so the editor renders state.
	//   true → same as nil.
	//   false → skip the stopped event; the host process keeps running.
	if args.StopOnEntry != nil && !*args.StopOnEntry {
		return
	}
	s.sendEvent("stopped", StoppedEventBody{
		Reason:            "entry",
		ThreadID:          1,
		AllThreadsStopped: true,
	})
}
