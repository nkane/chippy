package tui

import "fmt"

// disasmCtx is how many instructions the disassembly snapshot fetches above and
// below the anchor. Generous enough that any panel height finds the anchor plus
// the rows around it inside the snapshot; cheap over the inproc transport.
const disasmCtx = 48

// DisasmSnapshot is the instruction window the disassembly panel renders,
// sourced via a DAP `disassemble` round-trip (issue #452) — the panel no
// longer calls cpu.DisasmCPU itself. Lines are in ascending address order;
// Anchor is the address the window was centered on (PC when following, else
// the pinned scroll address).
type DisasmSnapshot struct {
	Lines  []disasmLine
	Anchor uint16
}

// disasmLine is one rendered instruction from the disassemble response.
type disasmLine struct {
	addr   uint16
	text   string // instruction mnemonic (or ".byte $XX" in a data range)
	symbol string // label at addr, "" if none
}

// fetchDisasm issues one `disassemble` request for [anchor-above, anchor+below]
// and parses the instruction lines. Transport-agnostic via remarshal.
func fetchDisasm(c dapRequester, anchor uint16, above, below int) (DisasmSnapshot, error) {
	resp, err := c.Request("disassemble", map[string]any{
		"memoryReference":   fmt.Sprintf("$%04X", anchor),
		"instructionOffset": -above,
		"instructionCount":  above + 1 + below,
	})
	if err != nil {
		return DisasmSnapshot{}, err
	}
	if !resp.Success {
		return DisasmSnapshot{}, fmt.Errorf("disassemble: %s", resp.Message)
	}
	var db struct {
		Instructions []struct {
			Address     string `json:"address"`
			Instruction string `json:"instruction"`
			Symbol      string `json:"symbol"`
		} `json:"instructions"`
	}
	if err := remarshal(resp.Body, &db); err != nil {
		return DisasmSnapshot{}, fmt.Errorf("disassemble body: %w", err)
	}
	ds := DisasmSnapshot{Anchor: anchor}
	for _, in := range db.Instructions {
		addr, ok := parseDollarHex16(in.Address)
		if !ok {
			continue
		}
		ds.Lines = append(ds.Lines, disasmLine{addr: addr, text: in.Instruction, symbol: in.Symbol})
	}
	return ds, nil
}

// syncDisasm refreshes m.Disasm from the active Source. Unlike the other
// panels it is not skipped during a remote free-run: there is no streamed
// disassembly, so the window is re-derived each tick from the DAP-fed mirror
// (via the inproc server — no wire round-trip) to follow the streamed PC.
// disasmView renders the cached snapshot, so View stays pure.
func (m *Model) syncDisasm() {
	if m.Source == nil {
		return
	}
	anchor := m.Regs.PC
	if !m.DisasmFollow {
		anchor = m.DisasmAnchor
	}
	if ds, err := m.Source.Disassemble(anchor, disasmCtx, disasmCtx); err == nil {
		m.Disasm = ds
	}
}
