package dap

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/peripheral"
	"github.com/nkane/chippy/symbols"
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
	Banked  *cpu.Banked24 // 65816 bank-aware bus; nil for 8/16-bit variants
	Tracer  *cpu.FileTracer
	Syms    *symbols.Table
	SrcMap  *symbols.SourceMap
	TextOut *peripheral.TextOutput
	KeyIn   *peripheral.KeyboardInput

	// CPUMu (optional) — shared mutex the hosting process also takes
	// before mutating the CPU / RAM / peripherals. The DAP server
	// acquires it around dispatch + every run-loop Step to serialize
	// concurrent access from the host (e.g. the TUI's `:dap` command).
	// nil = no concurrent access; server runs unlocked.
	CPUMu *sync.Mutex

	// OnAttached fires once after the editor's `attach` request
	// succeeds. Hosts use this to pause their own run loop while a
	// client is driving the CPU — e.g. nessy gates its game loop on
	// "has a client attached" so the only CPU stepper is the server's
	// `continue` runLoop. Optional; nil means "do nothing".
	OnAttached func()

	// OnDisconnected mirrors OnAttached. Fires when the server tears
	// down: either via a `disconnect` request or wire EOF in Serve().
	// Hosts resume their autonomous run loop. Optional.
	OnDisconnected func()

	// CustomRequestHandler handles DAP request commands the built-in
	// dispatch doesn't recognize — letting a host serve domain-specific
	// debug data (e.g. nessy's PPU / OAM / mapper state) over its own
	// `vendor/command` requests without forking the protocol. It runs
	// from dispatch's fallback path under the CPU lock (so reads of live
	// debuggee state are coherent). Return handled=false to defer to the
	// standard "not implemented" error; handled=true with a non-nil err
	// sends an error response, otherwise body is marshalled as the
	// response body. Optional; nil means unknown commands always error.
	CustomRequestHandler func(command string, args json.RawMessage) (body any, handled bool, err error)
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
	s.banked = cfg.Banked
	s.tracer = cfg.Tracer
	s.syms = cfg.Syms
	s.srcMap = cfg.SrcMap
	s.textOut = cfg.TextOut
	s.keyIn = cfg.KeyIn
	s.cpuMu = cfg.CPUMu
	s.onAttached = cfg.OnAttached
	s.onDisconnected = cfg.OnDisconnected
	s.customHandler = cfg.CustomRequestHandler
	return nil
}

// SetSymbols updates the server's symbol table + source map after attach.
// The TUI's in-process LocalSource (issue #449) builds its server in New —
// before the symbols are loaded via the WithSymbols / WithSourceMap builders
// — so it pushes them in afterwards, letting stackTrace frames carry callee
// names and source lines in local mode. Safe only before Serve()/dispatch
// races (the inproc client is synchronous and wired during construction).
func (s *Server) SetSymbols(syms *symbols.Table, srcMap *symbols.SourceMap) {
	if syms != nil {
		s.syms = syms
	}
	if srcMap != nil {
		s.srcMap = srcMap
	}
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
	// Record that a paired attach happened so fireDisconnected
	// later can decide whether to invoke the host's OnDisconnected
	// callback. Probe connections that never reach this point won't
	// see OnDisconnected either.
	s.attachedFired.Store(true)
	// Notify the host that a client has attached. Hosts use this to
	// gate their own run loops (e.g. nessy pauses its game loop while
	// the DAP server owns execution).
	if s.onAttached != nil {
		s.onAttached()
	}
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
