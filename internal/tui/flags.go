package tui

import (
	"fmt"

	"github.com/nkane/chippy/cpu"
)

// flagsFromP decomposes a raw P register into a FlagsSnapshot. Used only on
// the `chippy-state` live-stream fast path (issue #395), where both ends are
// chippy and the event already carries the raw P byte — so the Flags panel
// stays as live as the Registers panel during a remote free-run without a
// per-frame `variables` round-trip. The authoritative path (the Flags scope
// fetched on stop via fetchFlags) keeps the bit interpretation server-side.
func flagsFromP(p byte) FlagsSnapshot {
	return FlagsSnapshot{
		N: p&cpu.FlagN != 0,
		V: p&cpu.FlagV != 0,
		U: p&cpu.FlagU != 0,
		B: p&cpu.FlagB != 0,
		D: p&cpu.FlagD != 0,
		I: p&cpu.FlagI != 0,
		Z: p&cpu.FlagZ != 0,
		C: p&cpu.FlagC != 0,
	}
}

// FlagsSnapshot is the decomposed P-register state the Flags panel renders,
// populated through a DAP `variables` round-trip against the Flags scope
// (issue #450). Sourcing the individual bits from the server — rather than
// bit-testing m.CPU.P in the panel — keeps the TUI a generic DAP client: the
// server owns the P-bit interpretation. Local mode round-trips the in-process
// DAP server (sub-microsecond, #393); remote reuses the attach client.
type FlagsSnapshot struct {
	N, V, U, B, D, I, Z, C bool
}

// fetchFlags issues one `variables` request for the Flags scope (ref=2) and
// parses the eight bit values. Transport-agnostic via remarshal.
func fetchFlags(c dapRequester) (FlagsSnapshot, error) {
	resp, err := c.Request("variables", map[string]any{"variablesReference": 2})
	if err != nil {
		return FlagsSnapshot{}, err
	}
	if !resp.Success {
		return FlagsSnapshot{}, fmt.Errorf("variables(flags): %s", resp.Message)
	}
	var vb struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	if err := remarshal(resp.Body, &vb); err != nil {
		return FlagsSnapshot{}, fmt.Errorf("variables(flags) body: %w", err)
	}
	var fs FlagsSnapshot
	for _, v := range vb.Variables {
		on := v.Value == "1"
		switch v.Name {
		case "N":
			fs.N = on
		case "V":
			fs.V = on
		case "U":
			fs.U = on
		case "B":
			fs.B = on
		case "D":
			fs.D = on
		case "I":
			fs.I = on
		case "Z":
			fs.Z = on
		case "C":
			fs.C = on
		}
	}
	return fs, nil
}

// syncFlags refreshes m.Flags from the active Source. Mirrors syncRegs:
// skipped during a remote free-run (the server streams state and the stopped
// event reconciles), polled every tick locally through the inproc client.
// flagsView renders the cached snapshot, so View stays pure.
func (m *Model) syncFlags() {
	if m.Source == nil {
		return
	}
	if m.Running && m.Source.Attached() {
		return
	}
	if fs, err := m.Source.Flags(); err == nil {
		m.Flags = fs
	}
}
