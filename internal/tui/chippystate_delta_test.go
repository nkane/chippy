package tui

import (
	"testing"

	"github.com/nkane/chippy/dap"
)

// TestChippyState_AppliesDirtyRanges checks a chippy-state event's dirtyRanges
// are written into the mirror RAM so the memory panel updates live (issue
// #440), without waiting for the stopped-event full reconcile.
func TestChippyState_AppliesDirtyRanges(t *testing.T) {
	m := newWatchModel()
	ev := dap.Event{
		Event: dap.ChippyStateEvent,
		Body: dap.ChippyStateBody{
			PC: 0x8005,
			DirtyRanges: []dap.MemRange{
				{Start: 0x0300, End: 0x0303, Data: []byte{0xAA, 0xBB, 0xCC}},
			},
		},
	}
	m2, _ := m.Update(dapEventMsg{ev: ev})
	got := m2.(Model)
	for i, want := range []byte{0xAA, 0xBB, 0xCC} {
		if v := got.RAM.Read(0x0300 + uint16(i)); v != want {
			t.Errorf("RAM[$%04X] = $%02X; want $%02X", 0x0300+i, v, want)
		}
	}
	if got.Regs.PC != 0x8005 {
		t.Errorf("Regs.PC = $%04X; want $8005", got.Regs.PC)
	}
}
