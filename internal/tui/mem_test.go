package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

// TestSyncMem_FromDAP proves the memory panel reads DAP-sourced bytes (issue
// #451): New seeds m.MemView through the in-process readMemory, so memByte
// returns core memory fetched over the protocol — not a direct m.RAM.Read.
func TestSyncMem_FromDAP(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x0042, 0xAB)
	c := cpu.New(ram)

	m := New(c, ram) // MemViewAddr defaults to 0 -> window covers $0042

	if got := m.memByte(0x0042); got != 0xAB {
		t.Fatalf("memByte($0042) via DAP: want $AB, got $%02X", got)
	}
}

// TestSyncMem_ReanchorWindow covers a window near the top of memory: the
// clamp keeps the fetch inside $FFFF and the last byte is still readable.
func TestSyncMem_ReanchorWindow(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0xFFFF, 0x5A)
	c := cpu.New(ram)

	m := New(c, ram)
	m.MemViewAddr = 0xFF00
	m.syncMem()

	if got := m.memByte(0xFFFF); got != 0x5A {
		t.Fatalf("memByte($FFFF) via DAP: want $5A, got $%02X", got)
	}
}

// TestBank_SelectAndView proves the memory panel can view a 65816 bank ≠ 0:
// `:bank` selects it, and the DAP-sourced window then reads distinct storage
// (#505).
func TestBank_SelectAndView(t *testing.T) {
	ram := cpu.NewRAM()
	c := cpu.NewVariant(ram, cpu.VariantW65816)
	banked := cpu.NewBanked24(ram)
	c.SetBus24(banked)
	banked.Write24(0x031234, 0x5A) // bank 3
	ram.Write(0x1234, 0x11)        // bank 0, same offset

	m := New(c, ram).WithBanked24(banked)

	if got := m.runCommand("bank 03"); got != "mem bank -> $03" {
		t.Fatalf("bank cmd status: %q", got)
	}
	if m.MemViewBank != 0x03 {
		t.Fatalf("MemViewBank=$%02X want $03", m.MemViewBank)
	}
	m.runCommand("goto $1234")
	m.refreshMemWindow()
	if got := m.memByte(0x1234); got != 0x5A {
		t.Fatalf("bank-3 view byte = $%02X want $5A", got)
	}

	// Editing in bank 3 writes the banked store, not bank 0.
	m.MemCursor = 0x1234
	m.memWrite(m.MemCursor, 0x99)
	if got := banked.Read24(0x031234); got != 0x99 {
		t.Fatalf("bank-3 edit = $%02X want $99", got)
	}
	if got := ram.Read(0x1234); got != 0x11 {
		t.Fatalf("bank-3 edit leaked into bank 0: $%02X want $11", got)
	}
}

func TestMemByte_OutsideWindow(t *testing.T) {
	m := Model{MemViewBase: 0x0200, MemView: make([]byte, memWindow)}
	if got := m.memByte(0x0100); got != 0 {
		t.Fatalf("below window should read 0, got $%02X", got)
	}
	if got := m.memByte(0x0200 + memWindow); got != 0 {
		t.Fatalf("above window should read 0, got $%02X", got)
	}
}
