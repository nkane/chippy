package tui

import (
	"fmt"
	"strconv"

	"github.com/nkane/chippy/dap"
)

// RegSnapshot is the register state the Registers panel renders. It is
// populated exclusively through a DAP `variables` round-trip (issue #394) —
// the proof-of-concept for the TUI-via-DAP-only direction — so the panel never
// touches cpu.CPU fields directly. Local mode round-trips an in-process DAP
// server (sub-microsecond, #393); remote mode reuses the attach client.
type RegSnapshot struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Halted         bool
}

// dapRequester is the subset of the DAP client surface the register fetch
// needs. Both *dap.InprocClient (local) and *dap.Client (remote) satisfy it.
type dapRequester interface {
	Request(command string, args any) (dap.Response, error)
}

// fetchRegs issues one `variables` request for the Registers scope and parses
// A/X/Y/SP/PC/P/Cycles out of the response. Transport-agnostic: remarshal
// handles both the wire client's JSON body and the inproc client's struct.
func fetchRegs(c dapRequester) (RegSnapshot, error) {
	resp, err := c.Request("variables", map[string]any{"variablesReference": 1})
	if err != nil {
		return RegSnapshot{}, err
	}
	if !resp.Success {
		return RegSnapshot{}, fmt.Errorf("variables: %s", resp.Message)
	}
	var vb struct {
		Variables []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"variables"`
	}
	if err := remarshal(resp.Body, &vb); err != nil {
		return RegSnapshot{}, fmt.Errorf("variables body: %w", err)
	}
	var rs RegSnapshot
	for _, v := range vb.Variables {
		switch v.Name {
		case "A":
			rs.A, _ = parseDollarHex8(v.Value)
		case "X":
			rs.X, _ = parseDollarHex8(v.Value)
		case "Y":
			rs.Y, _ = parseDollarHex8(v.Value)
		case "SP":
			rs.SP, _ = parseDollarHex8(v.Value)
		case "P":
			rs.P, _ = parseDollarHex8(v.Value)
		case "PC":
			rs.PC, _ = parseDollarHex16(v.Value)
		case "Cycles":
			rs.Cycles, _ = strconv.ParseUint(v.Value, 10, 64)
		}
	}
	return rs, nil
}

// syncRegs refreshes m.Regs from the active Source. Called from the Update
// loop wherever CPU state can change (step / run tick / reset / rewind /
// trace frame / remote stopped). regsView renders the cached snapshot, so
// View stays pure.
func (m *Model) syncRegs() {
	if m.Source == nil {
		return
	}
	if rs, err := m.Source.Registers(); err == nil {
		m.Regs = rs
	}
}
