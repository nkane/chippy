package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/loader"
	"github.com/nkane/chippy/peripheral"
	"github.com/nkane/chippy/symbols"
)

// Server is a single DAP session. It owns the transport, the message-frame
// dispatch loop, and the debuggee state created on `launch`.
//
// Concurrency: the dispatch loop is single-goroutine. Writes go through
// `writeMu` so future async events emitted from background goroutines
// (e.g. the CPU run loop the step issue will add) don't tangle with
// in-progress responses.
type Server struct {
	in  *bufio.Reader
	out io.Writer

	// sink, when non-nil, receives Response/Event structs directly instead
	// of them being marshalled to the wire (the in-process transport — see
	// inproc.go). Lets a same-process client round-trip without JSON.
	sink func(any)

	writeMu sync.Mutex
	seq     int

	// Debuggee state — populated by `launch`. Nil before launch / after
	// disconnect. Each request handler checks for nil and reports a
	// useful error rather than panicking.
	cpu     *cpu.CPU
	ram     *cpu.RAM
	mmio    *cpu.MMIO
	tracer  *cpu.FileTracer
	syms    *symbols.Table
	srcMap  *symbols.SourceMap
	textOut *peripheral.TextOutput
	keyIn   *peripheral.KeyboardInput

	// terminated flips true on disconnect / terminate. Serve returns when
	// either the wire closes or this flag goes true.
	terminated bool

	// Step-control state. `running` is set by `continue`; the run loop
	// goroutine spins Step() in the background until it observes
	// `pauseRequested` (set by `pause` or by another step request) or
	// the CPU halts. `runDone` is closed by the run loop on exit so
	// other handlers can wait for it before mutating CPU state.
	running        atomic.Bool
	pauseRequested atomic.Bool
	runDone        chan struct{}

	// Breakpoint state. bpsBySrc maps source path -> line -> resolved PC;
	// bpsInst is the address-breakpoint set. bpHit is the flattened
	// PC-set the run loop checks each iteration (recomputed on every
	// set request). All three are guarded by bpMu since setBreakpoints
	// may arrive while the run loop is reading bpHit.
	bpMu         sync.Mutex
	bpsBySrc     map[string]map[int]uint16
	bpsInst      map[uint16]bool
	bpsByName    map[string]uint16
	bpHit        map[uint16]bool
	bpMetaBySrc  map[string]map[int]*bpMeta // parallel to bpsBySrc
	bpMetaInst   map[uint16]*bpMeta         // parallel to bpsInst
	bpMetaByName map[string]*bpMeta         // parallel to bpsByName
	bpMetaByPC   map[uint16]*bpMeta         // flattened union for run-loop

	// Reverse-step ring. Populated on every explicit-step request
	// (stepIn/next/stepOut) and on continue→bp stops. stepBack pops one
	// snapshot and restores. Same 256-entry ring the TUI uses for `<`.
	rewind *cpu.SnapshotRing

	// Exception-break state. `brkOnException` is set by
	// setExceptionBreakpoints when the "brk" filter is enabled; while
	// true, the run loop pauses just before executing a $00 (BRK)
	// opcode. `lastExceptionPC` is the PC reported by exceptionInfo.
	brkOnException  atomic.Bool
	lastExceptionPC atomic.Uint32

	// cpuMu (optional, set via AttachConfig.CPUMu) serializes CPU /
	// RAM / peripheral access with a concurrently-running TUI. nil
	// means single-surface mode; the locking helpers below are no-ops.
	cpuMu *sync.Mutex

	// onAttached / onDisconnected fire at the request lifecycle
	// boundaries: handleAttach success + disconnect/EOF. Hosts use
	// these to gate their own run loops (e.g. nessy pauses its game
	// loop while a client is attached so the server's `continue`
	// runLoop owns execution). nil = no-op. Set via AttachConfig.
	onAttached     func()
	onDisconnected func()

	// customHandler handles request commands the built-in dispatch
	// switch doesn't recognize. Set via AttachConfig.CustomRequestHandler.
	// Invoked from the dispatch `default` case under the CPU lock, so a
	// handler that reads live debuggee state observes a coherent,
	// mid-instruction-free snapshot. Returning handled=false falls back
	// to the standard "not implemented" error. Lets a host (e.g. nessy)
	// expose domain-specific debug state — PPU / OAM / mapper registers —
	// over its own `vendor/command` requests without forking the protocol.
	customHandler func(command string, args json.RawMessage) (body any, handled bool, err error)

	// attachedFired records whether handleAttach completed
	// successfully. fireDisconnected uses this to guarantee the host
	// only sees `OnDisconnected` when a paired `OnAttached` actually
	// happened — a probe TCP connection (e.g. the launcher's
	// listener-ready check that dials + closes without sending any
	// DAP traffic) opens a Server, never attaches, and gets EOF.
	// Without this guard the host would see a stray disconnect for a
	// session that never started, dropping any counters into negative
	// territory.
	attachedFired atomic.Bool

	// disconnectedFired guards onDisconnected against double-fire
	// when both Serve()'s EOF exit and a client-initiated disconnect
	// race to invoke it.
	disconnectedFired atomic.Bool
}

// lockCPU acquires s.cpuMu if present. Paired with unlockCPU. Used by
// the request dispatcher and the run-loop iteration body so TUI keys
// and DAP requests don't race on CPU state.
func (s *Server) lockCPU() {
	if s.cpuMu != nil {
		s.cpuMu.Lock()
	}
}

func (s *Server) unlockCPU() {
	if s.cpuMu != nil {
		s.cpuMu.Unlock()
	}
}

// NewServer wires the transport to a fresh Server. r/w must point at the
// negotiated DAP channel — stdio in stdio mode, the accepted TCP socket
// in tcp mode.
func NewServer(r io.Reader, w io.Writer) *Server {
	s := newServer()
	s.in = bufio.NewReader(r)
	s.out = w
	return s
}

// newServer builds a Server with its bookkeeping maps initialised but no
// transport bound. NewServer adds the wire reader/writer; NewInprocServer
// (inproc.go) adds the struct sink.
func newServer() *Server {
	return &Server{
		bpsBySrc:     map[string]map[int]uint16{},
		bpsInst:      map[uint16]bool{},
		bpsByName:    map[string]uint16{},
		bpHit:        map[uint16]bool{},
		bpMetaBySrc:  map[string]map[int]*bpMeta{},
		bpMetaInst:   map[uint16]*bpMeta{},
		bpMetaByName: map[string]*bpMeta{},
		bpMetaByPC:   map[uint16]*bpMeta{},
		rewind:       cpu.NewSnapshotRing(cpu.DefaultSnapshotRingCap),
	}
}

// Serve runs the dispatch loop until EOF, write failure, or a successful
// disconnect/terminate. Returns nil on a clean shutdown. Fires
// OnDisconnected exactly once on exit so the host can resume its own
// run loop.
func (s *Server) Serve() error {
	defer s.fireDisconnected()
	for {
		if s.terminated {
			return nil
		}
		body, err := ReadMessage(s.in)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
		var msg ProtocolMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			log.Printf("dap: drop unparseable message: %v", err)
			continue
		}
		if msg.Type != "request" {
			log.Printf("dap: ignore non-request type %q", msg.Type)
			continue
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			log.Printf("dap: drop unparseable request: %v", err)
			continue
		}
		s.dispatch(req)
	}
}

func (s *Server) dispatch(req Request) {
	// Lock around CPU-touching dispatch so a concurrently-running TUI
	// (via the `:dap` command) doesn't race. handleContinue and
	// handlePause manage their own ownership window — see notes there.
	if req.Command != "continue" && req.Command != "pause" {
		s.lockCPU()
		defer s.unlockCPU()
	}
	switch req.Command {
	case "initialize":
		s.handleInitialize(req)
	case "launch":
		s.handleLaunch(req)
	case "attach":
		s.handleAttach(req)
	case "configurationDone":
		s.sendResponse(req, nil)
	case "continue":
		s.handleContinue(req)
	case "next":
		s.handleNext(req)
	case "stepIn":
		s.handleStepIn(req)
	case "stepOut":
		s.handleStepOut(req)
	case "pause":
		s.handlePause(req)
	case "stepBack":
		s.handleStepBack(req)
	case "threads":
		s.handleThreads(req)
	case "stackTrace":
		s.handleStackTrace(req)
	case "scopes":
		s.handleScopes(req)
	case "variables":
		s.handleVariables(req)
	case "setVariable":
		s.handleSetVariable(req)
	case "setBreakpoints":
		s.handleSetBreakpoints(req)
	case "setInstructionBreakpoints":
		s.handleSetInstructionBreakpoints(req)
	case "setFunctionBreakpoints":
		s.handleSetFunctionBreakpoints(req)
	case "breakpointLocations":
		s.handleBreakpointLocations(req)
	case "disassemble":
		s.handleDisassemble(req)
	case "readMemory":
		s.handleReadMemory(req)
	case "writeMemory":
		s.handleWriteMemory(req)
	case "evaluate":
		s.handleEvaluate(req)
	case "loadedSources":
		s.handleLoadedSources(req)
	case "source":
		s.handleSource(req)
	case "completions":
		s.handleCompletions(req)
	case "setExceptionBreakpoints":
		s.handleSetExceptionBreakpoints(req)
	case "exceptionInfo":
		s.handleExceptionInfo(req)
	case "disconnect":
		s.handleDisconnect(req)
	case "terminate":
		s.handleTerminate(req)
	default:
		if s.customHandler != nil {
			body, handled, err := s.customHandler(req.Command, req.Arguments)
			if handled {
				if err != nil {
					s.sendErrorResponse(req, err.Error())
				} else {
					s.sendResponse(req, body)
				}
				return
			}
		}
		s.sendErrorResponse(req, fmt.Sprintf("not implemented: %s", req.Command))
	}
}

func (s *Server) handleInitialize(req Request) {
	// initialize response embeds the exception-filter list. Defined here
	// rather than carried on Capabilities so it stays close to the
	// initialize handler — Capabilities is a flat bool struct.
	type capsWithFilters struct {
		Capabilities
		ExceptionBreakpointFilters []ExceptionBreakpointsFilter `json:"exceptionBreakpointFilters,omitempty"`
	}
	caps := Capabilities{
		SupportsConfigurationDoneRequest:   true,
		SupportsConditionalBreakpoints:     true,
		SupportsHitConditionalBreakpoints:  true,
		SupportsEvaluateForHovers:          true,
		SupportsTerminateRequest:           true,
		SupportsDisassembleRequest:         true,
		SupportsReadMemoryRequest:          true,
		SupportsWriteMemoryRequest:         true,
		SupportsInstructionBreakpoints:     true,
		SupportsLogPoints:                  true,
		SupportsBreakpointLocationsRequest: true,
		SupportsLoadedSourcesRequest:       true,
		SupportsStepBack:                   true,
		SupportsFunctionBreakpoints:        true,
		SupportsRestartRequest:             false,
		SupportsCompletionsRequest:         true,
		SupportsSetVariable:                true,
		SupportsExceptionInfoRequest:       true,
	}
	resp := capsWithFilters{
		Capabilities: caps,
		ExceptionBreakpointFilters: []ExceptionBreakpointsFilter{
			{
				Filter:      "brk",
				Label:       "BRK ($00 opcode)",
				Description: "Pause just before the CPU executes a BRK instruction.",
			},
		},
	}
	s.sendResponse(req, resp)
	s.sendEvent("initialized", nil)
}

func (s *Server) handleLaunch(req Request) {
	var args LaunchArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad launch args: %v", err))
		return
	}
	if err := s.bootDebuggee(args); err != nil {
		s.sendErrorResponse(req, err.Error())
		return
	}
	s.sendResponse(req, nil)
	if args.NoDebug {
		return
	}
	// Default behavior: pause at the reset vector and report it so the
	// client immediately renders state. An explicit `stopOnEntry: false`
	// in the launch arguments overrides — auto-start the run loop in
	// place of the stopped event.
	if args.StopOnEntry != nil && !*args.StopOnEntry {
		s.autoStartRun()
		return
	}
	s.sendEvent("stopped", StoppedEventBody{
		Reason:            "entry",
		ThreadID:          1,
		AllThreadsStopped: true,
	})
}

// autoStartRun kicks off the run-loop goroutine after a launch/attach
// when the client opted out of the entry pause. Mirrors handleContinue
// but doesn't send a response (there's no `continue` request in flight).
func (s *Server) autoStartRun() {
	if s.running.Load() {
		return
	}
	s.pauseRequested.Store(false)
	s.runDone = make(chan struct{})
	s.running.Store(true)
	go s.runLoop()
}

// bootDebuggee constructs the CPU+RAM+MMIO chain the same way
// cmd/chippy/main.go does for the TUI. Returns a launch-shaped error
// message on any wiring failure so it can be relayed to the client.
func (s *Server) bootDebuggee(args LaunchArguments) error {
	if s.cpu != nil {
		return fmt.Errorf("a launch is already active; disconnect first")
	}
	var variant cpu.Variant
	switch strings.ToLower(args.CPUVariant) {
	case "", "nmos", "6502":
		variant = cpu.VariantNMOS
	case "65c02", "cmos", "cmos65c02":
		variant = cpu.VariantCMOS65C02
	case "nes", "2a03", "ricoh":
		variant = cpu.VariantNES
	default:
		return fmt.Errorf("unknown cpuVariant %q", args.CPUVariant)
	}
	ram := cpu.NewRAM()
	var loaded *loader.Result
	if args.Rom != "" {
		var err error
		loaded, err = loader.Load(ram, args.Rom, loader.Options{
			Addr:      args.LoadAddr,
			LinkerCfg: args.LinkerCfg,
		})
		if err != nil {
			return fmt.Errorf("load rom: %w", err)
		}
	} else {
		return fmt.Errorf("launch requires a `rom` argument")
	}
	switch {
	case args.ResetVec != 0:
		ram.Write(cpu.VecReset, byte(args.ResetVec))
		ram.Write(cpu.VecReset+1, byte(args.ResetVec>>8))
	case ram.Read(cpu.VecReset) == 0 && ram.Read(cpu.VecReset+1) == 0:
		ram.Write(cpu.VecReset, byte(loaded.LoadAddr))
		ram.Write(cpu.VecReset+1, byte(loaded.LoadAddr>>8))
	}

	dbg := args.DbgPath
	if dbg == "" {
		if loaded.LinkedBin != "" {
			dbg = symbols.SiblingDbg(loaded.LinkedBin)
		}
		if dbg == "" {
			dbg = symbols.SiblingDbg(args.Rom)
		}
	}
	if dbg != "" {
		if t, err := symbols.LoadDbg(dbg); err == nil {
			s.syms = t
		}
		if sm, err := symbols.LoadSourceMap(dbg); err == nil {
			s.srcMap = sm
		}
	}

	mmio := cpu.NewMMIO(ram)
	textOut := peripheral.NewTextOutput(0xF001)
	keyIn := peripheral.NewKeyboardInput(0xF004, 0xF005)
	if err := mmio.Register(textOut); err != nil {
		return fmt.Errorf("register text output: %w", err)
	}
	if err := mmio.Register(keyIn); err != nil {
		return fmt.Errorf("register keyboard: %w", err)
	}

	c := cpu.NewVariant(mmio, variant)
	tracer := cpu.NewFileTracer()
	c.Tracer = tracer
	if args.TracePath != "" {
		if err := tracer.SetPath(args.TracePath); err != nil {
			return fmt.Errorf("trace: %w", err)
		}
		tracer.Enable()
	}

	s.cpu = c
	s.ram = ram
	s.ram.EnableShadow() // CoW page tracking powers stepBack (issue #66).
	s.mmio = mmio
	s.tracer = tracer
	s.textOut = textOut
	s.keyIn = keyIn
	return nil
}

func (s *Server) handleDisconnect(req Request) {
	s.stopRunLoop()
	s.sendResponse(req, nil)
	if s.tracer != nil {
		_ = s.tracer.Close()
	}
	s.terminated = true
	s.fireDisconnected()
}

func (s *Server) handleTerminate(req Request) {
	s.stopRunLoop()
	s.sendResponse(req, nil)
	s.sendEvent("terminated", TerminatedEventBody{})
	if s.tracer != nil {
		_ = s.tracer.Close()
	}
	s.terminated = true
	s.fireDisconnected()
}

// fireDisconnected invokes the host's OnDisconnected callback (if any)
// exactly once per Server, AND only if a paired OnAttached has
// actually fired. The `disconnectedFired` guard handles the
// case where Serve()'s EOF path and a client-initiated disconnect
// request both try to fire it; the `attachedFired` guard skips the
// callback entirely for probe-style connections that never sent an
// attach request.
func (s *Server) fireDisconnected() {
	if s.onDisconnected == nil {
		return
	}
	if !s.attachedFired.Load() {
		return
	}
	if s.disconnectedFired.Swap(true) {
		return
	}
	s.onDisconnected()
}

// stopRunLoop signals the run-loop goroutine (if any) to exit and waits
// for it to finish so callers can safely tear down CPU state. No-op when
// the CPU is already stopped.
func (s *Server) stopRunLoop() {
	if !s.running.Load() {
		return
	}
	s.pauseRequested.Store(true)
	<-s.runDone
}

// sendResponse marshals a successful response for req. body may be nil.
func (s *Server) sendResponse(req Request, body interface{}) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.seq++
	resp := Response{
		ProtocolMessage: ProtocolMessage{Seq: s.seq, Type: "response"},
		RequestSeq:      req.Seq,
		Success:         true,
		Command:         req.Command,
		Body:            body,
	}
	s.writeJSON(resp)
}

func (s *Server) sendErrorResponse(req Request, message string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.seq++
	resp := Response{
		ProtocolMessage: ProtocolMessage{Seq: s.seq, Type: "response"},
		RequestSeq:      req.Seq,
		Success:         false,
		Command:         req.Command,
		Message:         message,
	}
	s.writeJSON(resp)
}

func (s *Server) sendEvent(name string, body interface{}) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.seq++
	ev := Event{
		ProtocolMessage: ProtocolMessage{Seq: s.seq, Type: "event"},
		Event:           name,
		Body:            body,
	}
	s.writeJSON(ev)
}

func (s *Server) writeJSON(v interface{}) {
	if s.sink != nil {
		s.sink(v)
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("dap: marshal failed: %v", err)
		return
	}
	if err := WriteMessage(s.out, data); err != nil {
		log.Printf("dap: write failed: %v", err)
	}
}
