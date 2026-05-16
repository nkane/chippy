package tui

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/dap"
)

// remoteThreadID is the conventional DAP thread ID chippy's server
// reports — single CPU, single thread, fixed at 1. Mirrors what
// `internal/dap/steps.go::handleThreads` advertises.
const remoteThreadID = 1

// remoteStepTimeout caps how long Step / Continue / Pause wait for the
// expected `stopped` event. Generous enough to absorb a slow link or
// a long breakpoint walk, short enough that a wedged server doesn't
// hang the TUI forever — the user can then Esc out and reconnect.
const remoteStepTimeout = 5 * time.Second

// RemoteSource drives a CPU on the other end of a DAP connection. The
// TUI's display panels keep reading `m.CPU` directly; RemoteSource
// writes the mirror after every step so those panels see a consistent
// post-operation state.
//
// Concurrency:
//   - One goroutine demuxes `dap.Client.Events()` into two internal
//     channels: `stopped` (consumed by Step / waitForStop) and
//     `external` (forwarded to the TUI via Events()).
//   - All public methods are safe to call from the Bubble Tea
//     goroutine; under the hood they issue synchronous DAP requests.
type RemoteSource struct {
	client *dap.Client
	cpu    *cpu.CPU
	ram    *cpu.RAM
	addr   string

	stopped  chan dap.Event
	external chan dap.Event
	closed   atomic.Bool
}

// NewRemoteSource wraps a connected `dap.Client` and starts the event
// demux goroutine. `c` and `r` are the mirror CPU + RAM the TUI
// already holds — RemoteSource writes them after every operation so
// display code keeps reading from the same fields it used in local
// mode.
func NewRemoteSource(client *dap.Client, c *cpu.CPU, r *cpu.RAM, addr string) *RemoteSource {
	s := &RemoteSource{
		client:   client,
		cpu:      c,
		ram:      r,
		addr:     addr,
		stopped:  make(chan dap.Event, 16),
		external: make(chan dap.Event, 64),
	}
	go s.demuxEvents()
	return s
}

// Events returns the TUI-facing event stream. The TUI subscribes via a
// `tea.Cmd` that reads from this channel. The channel closes when the
// underlying connection ends.
func (s *RemoteSource) Events() <-chan dap.Event { return s.external }

func (s *RemoteSource) demuxEvents() {
	defer close(s.stopped)
	defer close(s.external)
	for ev := range s.client.Events() {
		if s.closed.Load() {
			return
		}
		// `stopped` events need to reach TWO places:
		//   - s.stopped — consumed by Step's waitForStop for the
		//     request-response synchronization of a single stepIn.
		//   - s.external — consumed by the TUI's dapEventMsg pump
		//     so the source-view, registers, and run flag refresh
		//     when the server stops mid-continue (breakpoint hit,
		//     pause request, exception).
		// Earlier routing sent stopped events ONLY to s.stopped, so
		// after `continue` + bp-hit the TUI never refreshed until
		// the user toggled state manually — the entire screen
		// looked frozen.
		if ev.Event == "stopped" {
			select {
			case s.stopped <- ev:
			default:
			}
		}
		select {
		case s.external <- ev:
		default:
		}
	}
}

// Step sends a DAP `stepIn` and waits for the matching `stopped`
// event, then syncs the mirror's CPU registers. Returns 0 since DAP
// doesn't expose cycle cost from a step (only the cumulative `Cycles`
// register, which is refreshed into the mirror).
func (s *RemoteSource) Step() int {
	if _, err := s.client.Request("stepIn", map[string]any{"threadId": remoteThreadID}); err != nil {
		return 0
	}
	if !s.waitForStop() {
		return 0
	}
	_ = s.refreshRegs()
	_ = s.RefreshMemory()
	return 0
}

// Reset is best-effort over DAP: chippy's server doesn't advertise a
// `restart` capability, so this is a no-op. The TUI's R key path
// guards on `m.Source.Attached()` and refuses to reset in remote
// mode rather than silently doing nothing.
func (s *RemoteSource) Reset() {
	// no-op; see comment above.
}

// StepBack sends a DAP `stepBack` if the server's capabilities allow,
// then syncs the mirror. Returns true on success.
func (s *RemoteSource) StepBack() bool {
	if _, err := s.client.Request("stepBack", map[string]any{"threadId": remoteThreadID}); err != nil {
		return false
	}
	if !s.waitForStop() {
		return false
	}
	_ = s.refreshRegs()
	_ = s.RefreshMemory()
	return true
}

// Continue starts the remote CPU running. The server emits `stopped`
// when a breakpoint hits or the CPU halts; the TUI watches Events()
// for that.
func (s *RemoteSource) Continue() error {
	_, err := s.client.Request("continue", map[string]any{"threadId": remoteThreadID})
	return err
}

// Pause halts a running remote CPU. Server emits `stopped` afterward,
// which the TUI picks up to refresh the mirror.
func (s *RemoteSource) Pause() error {
	_, err := s.client.Request("pause", map[string]any{"threadId": remoteThreadID})
	return err
}

// SetBreakpoints (re)installs the active PC breakpoint set on the
// server via `setInstructionBreakpoints`. Sends the full list each
// time — the server treats every call as authoritative.
func (s *RemoteSource) SetBreakpoints(pcs []uint16) error {
	type instBP struct {
		InstructionReference string `json:"instructionReference"`
	}
	bps := make([]instBP, 0, len(pcs))
	for _, pc := range pcs {
		bps = append(bps, instBP{InstructionReference: fmt.Sprintf("$%04X", pc)})
	}
	args := map[string]any{"breakpoints": bps}
	_, err := s.client.Request("setInstructionBreakpoints", args)
	return err
}

// RefreshRegs forces a regs sync. Called by the TUI after a `stopped`
// event arrives via Events() so the mirror catches up with the new
// PC.
func (s *RemoteSource) RefreshRegs() error { return s.refreshRegs() }

// RefreshMemory pulls the full CPU-bus memory ($0000-$FFFF) via DAP
// `readMemory` and writes it into the mirror's RAM so display panels
// (disasm, memory) render correct values. Called once on attach and
// then on every `stopped` event (CPU may have written RAM between
// stops).
//
// 64 KiB per fetch is small enough to feel instant over localhost
// (~64 KiB / 100+ MB/s = <1 ms); over a real network the request
// would benefit from a per-page lazy cache, but that's a v0.x
// refinement.
//
// For NROM cartridges the $8000-$FFFF range never changes after
// reset; for bank-switching mappers (MMC1+, v0.3+ work) this
// per-stop refresh keeps the mirror current automatically.
func (s *RemoteSource) RefreshMemory() error {
	resp, err := s.client.Request("readMemory", map[string]any{
		"memoryReference": "$0000",
		"offset":          0,
		"count":           0x10000,
	})
	if err != nil {
		return fmt.Errorf("readMemory: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("readMemory: %s", resp.Message)
	}
	var body struct {
		Address string `json:"address"`
		Data    string `json:"data"`
	}
	if err := remarshal(resp.Body, &body); err != nil {
		return fmt.Errorf("readMemory body: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		return fmt.Errorf("readMemory base64 decode: %w", err)
	}
	if len(decoded) > 0x10000 {
		decoded = decoded[:0x10000]
	}
	s.ram.Load(0, decoded)
	return nil
}

// Attached returns true for RemoteSource.
func (s *RemoteSource) Attached() bool { return true }

// Address returns the TCP address the client dialed (used by the
// status bar).
func (s *RemoteSource) Address() string { return s.addr }

// Close tears down the DAP client. Disconnect is sent first so the
// server can shut down cleanly; failure (e.g. already terminated) is
// ignored. Safe to call multiple times.
func (s *RemoteSource) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	_ = s.client.Disconnect()
	return s.client.Close()
}

// waitForStop blocks until a `stopped` event arrives or the timeout
// fires. Returns false on timeout / disconnect — callers should treat
// that as an aborted operation.
func (s *RemoteSource) waitForStop() bool {
	select {
	case _, ok := <-s.stopped:
		return ok
	case <-time.After(remoteStepTimeout):
		return false
	}
}

// refreshRegs pulls the live register state from the server and
// writes it into the mirror.
//
//	stackTrace → top frame's instructionPointerReference → PC
//	scopes / variables → A / X / Y / SP / P / Cycles
func (s *RemoteSource) refreshRegs() error {
	type stackFrame struct {
		ID                          int    `json:"id"`
		Name                        string `json:"name"`
		InstructionPointerReference string `json:"instructionPointerReference"`
	}
	type stackTraceBody struct {
		StackFrames []stackFrame `json:"stackFrames"`
		TotalFrames int          `json:"totalFrames"`
	}
	resp, err := s.client.Request("stackTrace", map[string]any{
		"threadId": remoteThreadID, "startFrame": 0, "levels": 1,
	})
	if err != nil {
		return fmt.Errorf("stackTrace: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("stackTrace: %s", resp.Message)
	}
	var st stackTraceBody
	if err := remarshal(resp.Body, &st); err != nil {
		return fmt.Errorf("stackTrace body: %w", err)
	}
	if len(st.StackFrames) > 0 {
		if pc, ok := parseDollarHex16(st.StackFrames[0].InstructionPointerReference); ok {
			s.cpu.PC = pc
		}
	}

	// Variables — ask for the Registers scope (variablesReference 1
	// per dap/vars.go). Skip the scopes request since the ref ID is
	// fixed.
	type variable struct {
		Name  string `json:"name"`
		Value string `json:"value"`
		Type  string `json:"type"`
	}
	type variablesBody struct {
		Variables []variable `json:"variables"`
	}
	resp, err = s.client.Request("variables", map[string]any{"variablesReference": 1})
	if err != nil {
		return fmt.Errorf("variables: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("variables: %s", resp.Message)
	}
	var vb variablesBody
	if err := remarshal(resp.Body, &vb); err != nil {
		return fmt.Errorf("variables body: %w", err)
	}
	for _, v := range vb.Variables {
		switch v.Name {
		case "A":
			if b, ok := parseDollarHex8(v.Value); ok {
				s.cpu.A = b
			}
		case "X":
			if b, ok := parseDollarHex8(v.Value); ok {
				s.cpu.X = b
			}
		case "Y":
			if b, ok := parseDollarHex8(v.Value); ok {
				s.cpu.Y = b
			}
		case "SP":
			if b, ok := parseDollarHex8(v.Value); ok {
				s.cpu.SP = b
			}
		case "P":
			if b, ok := parseDollarHex8(v.Value); ok {
				s.cpu.P = b
			}
		case "Cycles":
			if n, err := strconv.ParseUint(v.Value, 10, 64); err == nil {
				s.cpu.Cycles = n
			}
		}
	}
	return nil
}

// remarshal round-trips a JSON-decoded `interface{}` into a typed
// struct. The DAP Response.Body is `any` (whatever
// `json.Unmarshal` produced — typically a `map[string]any`); this
// avoids reflective field-by-field projection.
func remarshal(v any, dst any) error {
	if v == nil {
		return errors.New("nil body")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

// parseDollarHex16 parses "$XXXX" → uint16; returns false on
// malformed input.
func parseDollarHex16(s string) (uint16, bool) {
	s = strings.TrimPrefix(s, "$")
	n, err := strconv.ParseUint(s, 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(n), true
}

// parseDollarHex8 parses "$XX" → byte; returns false on malformed
// input.
func parseDollarHex8(s string) (byte, bool) {
	s = strings.TrimPrefix(s, "$")
	n, err := strconv.ParseUint(s, 16, 8)
	if err != nil {
		return 0, false
	}
	return byte(n), true
}

// Compile-time check.
var _ Source = (*RemoteSource)(nil)
