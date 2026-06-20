package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

// TestSyncDisasm_FromDAP proves the disassembly panel snapshot comes through
// the DAP disassemble request (issue #452): New seeds m.Disasm via the
// in-process server, so the instruction text is server-decoded — the panel
// never calls cpu.DisasmCPU itself.
func TestSyncDisasm_FromDAP(t *testing.T) {
	ram := cpu.NewRAM()
	// LDA #$42 ; NOP at the reset entry.
	ram.Write(0x0000, 0xA9)
	ram.Write(0x0001, 0x42)
	ram.Write(0x0002, 0xEA)
	c := cpu.New(ram)
	c.PC = 0x0000

	m := New(c, ram) // syncDisasm seeds around PC=$0000

	var line0 string
	for _, ln := range m.Disasm.Lines {
		if ln.addr == 0x0000 {
			line0 = ln.text
		}
	}
	if !strings.Contains(line0, "LDA") {
		t.Fatalf("disasm $0000 via DAP: want LDA…, got %q (lines: %+v)", line0, m.Disasm.Lines)
	}
}

// TestDisasmView_RendersSnapshot confirms the rendered panel shows the
// DAP-sourced instruction text and the PC marker.
func TestDisasmView_RendersSnapshot(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x0000, 0xEA) // NOP
	ram.Write(0x0001, 0xEA) // NOP
	c := cpu.New(ram)
	c.PC = 0x0000

	m := New(c, ram)
	out := m.disasmView(40, 12)
	if !strings.Contains(out, "NOP") {
		t.Fatalf("disasmView should render DAP-sourced NOP:\n%s", out)
	}
	if !strings.Contains(out, "$0000") {
		t.Fatalf("disasmView should show the PC address $0000:\n%s", out)
	}
}
