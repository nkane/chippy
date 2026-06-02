package tui

import (
	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/dap"
)

// Source abstracts the CPU + bus pair the TUI drives. LocalSource (this
// file) is a thin wrapper around the in-process chippy CPU and RAM, so
// every existing local-mode path keeps its current performance / shape.
// RemoteSource (source_remote.go) wraps a `dap.Client` and pushes every
// control operation across the wire — used by `chippy -dap-attach` to
// drive a remote nessy / chippy DAP server's CPU.
//
// Mirror model. The TUI keeps `m.CPU` + `m.RAM` populated in both modes
// so the display panels (register, disasm, memory, stack) can read
// fields directly. For local mode the mirror IS the live CPU. For
// remote mode the mirror is a stand-in whose register fields the
// RemoteSource refreshes after every Source operation, and whose RAM
// pages are pulled via DAP `readMemory` on demand.
//
// Concurrency. Source methods may be invoked from the Bubble Tea
// goroutine (key handling) or background goroutines (DAP event readers
// in RemoteSource). LocalSource serializes step + restore through the
// existing `CPUMu` if non-nil; RemoteSource is responsible for its own
// in-flight request synchronization.
type Source interface {
	// Step advances the CPU one instruction. After Step returns, the
	// TUI's mirror (`m.CPU`) reflects the post-step state. Returns
	// the cycle cost of the executed instruction. May push a snapshot
	// to the rewind ring (LocalSource — controlled internally;
	// RemoteSource skips since stepBack is the server's job).
	Step() int

	// Reset resets the CPU's PC + flags + cycles. Mirror refreshed.
	Reset()

	// StepBack rewinds one instruction. Returns false if the source
	// can't rewind (no snapshot history, or remote server lacks
	// stepBack capability). On success the mirror reflects the
	// rewound state.
	StepBack() bool

	// Continue puts the source into free-run mode. LocalSource is a
	// no-op (the TUI's `tickMsg` loop drives Step). RemoteSource
	// sends a DAP `continue` so the server takes over execution.
	Continue() error

	// Pause halts free-run. Mirror of Continue. LocalSource is a
	// no-op; the TUI clears `m.Running` itself. RemoteSource sends
	// DAP `pause`.
	Pause() error

	// SetBreakpoints (re)installs the active breakpoint set on the
	// source. LocalSource only needs the PCs (conditions evaluate
	// inside the TUI's run loop against the live CPU). RemoteSource
	// forwards via DAP `setInstructionBreakpoints` with the optional
	// `condition` field so the server can short-circuit non-matching
	// hits without a TUI round-trip.
	// Called whenever the TUI's `m.Breakpoints` map mutates so the
	// remote stays in sync.
	SetBreakpoints(bps []SourceBP) error

	// Attached reports whether this is a remote (DAP-backed) source.
	// Used by the status bar and to gate features that don't work
	// over DAP yet (full-rewind, certain rewind-ring tricks).
	Attached() bool

	// Address returns the remote server address ("tcp:HOST:PORT") or
	// "" for a local source. Surfaced in the status bar.
	Address() string

	// Close tears down any background connections. Safe to call
	// multiple times.
	Close() error

	// Events returns the asynchronous server-event stream for remote
	// sources (`stopped`, `terminated`, `output`, etc.). LocalSource
	// returns nil — local mode has no async events. The TUI subscribes
	// via a `tea.Cmd` that reads from this channel.
	Events() <-chan dap.Event

	// RefreshRegs forces a mirror-sync of CPU registers. LocalSource
	// is a no-op (the mirror IS the live CPU); RemoteSource issues
	// the DAP requests to pull PC + regs and writes them into the
	// mirror.
	RefreshRegs() error

	// RefreshMemory forces a mirror-sync of CPU bus memory.
	// LocalSource is a no-op (mirror IS live RAM); RemoteSource
	// issues a DAP `readMemory` request and writes the bytes into
	// the mirror's RAM so display panels (disasm, memory) render
	// correct values instead of zero-byte BRK chains. Called on
	// every `stopped` event + once on attach.
	RefreshMemory() error
}

// LocalSource is the in-process Source backing the default TUI mode.
// It holds direct references to the CPU + RAM the TUI already
// displays.
//
// LocalSource is intentionally narrow: it advances or restores the
// CPU + RAM and that's it. Rewind-ring management, CPUMu locking,
// peripheral snapshotting, and breakpoint maps all live on the Model
// side so the existing concurrency story stays exactly where it is.
// RemoteSource (source_remote.go) keeps the same narrow surface and
// pushes operations across DAP.
type LocalSource struct {
	cpu *cpu.CPU
	ram *cpu.RAM
}

// NewLocalSource builds the default Source wired to a real CPU + RAM.
func NewLocalSource(c *cpu.CPU, r *cpu.RAM) *LocalSource {
	return &LocalSource{cpu: c, ram: r}
}

// Step advances the CPU one instruction.
func (s *LocalSource) Step() int { return s.cpu.Step() }

// Reset resets the CPU.
func (s *LocalSource) Reset() { s.cpu.Reset() }

// StepBack is a no-op for LocalSource — the Model manages the rewind
// ring itself and pops directly. Returns false to signal "didn't do
// it via the Source"; the Model uses its own ring instead. The
// method exists for RemoteSource's benefit.
func (s *LocalSource) StepBack() bool { return false }

// RestoreSnapshot applies a snapshot to the local CPU + RAM. Used by
// the Model's rewind handler. Not part of the Source interface — only
// LocalSource exposes it because remote snapshot restore isn't a
// thing.
func (s *LocalSource) RestoreSnapshot(snap cpu.Snapshot) {
	s.cpu.Restore(snap, s.ram)
}

// Continue is a no-op for LocalSource — the TUI's tickMsg loop is what
// actually drives Step() during free-run; the run flag lives in the
// Model.
func (s *LocalSource) Continue() error { return nil }

// Pause is a no-op for LocalSource. Mirror of Continue.
func (s *LocalSource) Pause() error { return nil }

// SetBreakpoints is a no-op for LocalSource — the Model's
// `m.Breakpoints` map is the source of truth, checked inline by the
// run loop via `shouldBreakAt`. The interface still requires the
// method so RemoteSource can forward the same list to the remote
// server.
func (s *LocalSource) SetBreakpoints(bps []SourceBP) error { return nil }

// SourceBP is the wire shape Source.SetBreakpoints uses. PC is the
// instruction address; Cond is the raw expression string ("" =
// unconditional); HitLimit mirrors Breakpoint.HitLimit (0 unlimited,
// >0 break on/after Nth hit, -1 one-shot); Log is the log-point
// message ("" = pause-style bp).
type SourceBP struct {
	PC       uint16
	Cond     string
	HitLimit int
	Log      string
}

// Attached returns false for LocalSource.
func (s *LocalSource) Attached() bool { return false }

// Address returns "" for LocalSource.
func (s *LocalSource) Address() string { return "" }

// Close is a no-op for LocalSource — nothing to tear down.
func (s *LocalSource) Close() error { return nil }

// Events returns nil — local mode has no async events; the TUI's
// `tea.Cmd` will see a nil channel and skip the subscription.
func (s *LocalSource) Events() <-chan dap.Event { return nil }

// RefreshRegs is a no-op for LocalSource — the mirror IS the live CPU.
func (s *LocalSource) RefreshRegs() error { return nil }

// RefreshMemory is a no-op for LocalSource — the mirror IS the live RAM.
func (s *LocalSource) RefreshMemory() error { return nil }

// Compile-time check.
var _ Source = (*LocalSource)(nil)
