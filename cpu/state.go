package cpu

import (
	"errors"
	"sort"
)

// errBadStateSize is the canonical "save-state payload length doesn't
// match this version's struct" error. Wrapped by the package's
// LoadFullState entry points so a malformed disk save fails fast
// instead of half-writing memory.
var errBadStateSize = errors.New("save-state payload length mismatch")

// FullState is the gob-serializable full-CPU capture used by nessy's
// save-state system (#266). Distinct from the per-step Snapshot used
// by the TUI reverse-step ring — FullState carries everything needed
// to cold-restore a CPU from disk, the existing Snapshot is a
// page-delta epoch optimized for single-instruction undo.
//
// Bus / opcode table / tracer aren't part of the state; they're
// re-bound from whatever Bus the post-restore CPU is wired to.
type FullState struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Variant        Variant
	Halted         bool
	StoppedBySTP   bool
	ExtraCycles    int
	IRQLine        bool
	NMIPending     bool
	NMIDue         bool // NES interrupt-poll latch (#342)
	PendingStall   int
	IRQSources     []string // sorted for deterministic round-trip
}

// SaveFullState captures the live CPU state. Safe to call any time
// the CPU isn't mid-Step (the typical save-state cadence is between
// frames with cpuMu held).
func (c *CPU) SaveFullState() FullState {
	st := FullState{
		A:            c.A,
		X:            c.X,
		Y:            c.Y,
		SP:           c.SP,
		P:            c.P,
		PC:           c.PC,
		Cycles:       c.Cycles,
		Variant:      c.Variant,
		Halted:       c.Halted,
		StoppedBySTP: c.stoppedBySTP,
		ExtraCycles:  c.extraCycles,
		IRQLine:      c.irqLine,
		NMIPending:   c.nmiPending,
		NMIDue:       c.nmiDue,
		PendingStall: c.pendingStall,
	}
	if len(c.irqSources) > 0 {
		st.IRQSources = make([]string, 0, len(c.irqSources))
		for name := range c.irqSources {
			st.IRQSources = append(st.IRQSources, name)
		}
		sort.Strings(st.IRQSources)
	}
	return st
}

// LoadFullState overwrites this CPU's state from s. Bus + tracer stay
// connected. Re-binds the opcode table from s.Variant so a save taken
// under NMOS can't accidentally run as CMOS post-restore.
func (c *CPU) LoadFullState(s FullState) {
	c.A = s.A
	c.X = s.X
	c.Y = s.Y
	c.SP = s.SP
	c.P = s.P
	c.PC = s.PC
	c.Cycles = s.Cycles
	c.Variant = s.Variant
	c.Halted = s.Halted
	c.stoppedBySTP = s.StoppedBySTP
	c.extraCycles = s.ExtraCycles
	c.irqLine = s.IRQLine
	c.nmiPending = s.NMIPending
	c.nmiDue = s.NMIDue
	c.pendingStall = s.PendingStall
	c.irqSources = nil
	if len(s.IRQSources) > 0 {
		c.irqSources = make(map[string]struct{}, len(s.IRQSources))
		for _, name := range s.IRQSources {
			c.irqSources[name] = struct{}{}
		}
	}
	c.bindTable()
}
