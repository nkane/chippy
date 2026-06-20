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

func TestMemByte_OutsideWindow(t *testing.T) {
	m := Model{MemViewBase: 0x0200, MemView: make([]byte, memWindow)}
	if got := m.memByte(0x0100); got != 0 {
		t.Fatalf("below window should read 0, got $%02X", got)
	}
	if got := m.memByte(0x0200 + memWindow); got != 0 {
		t.Fatalf("above window should read 0, got $%02X", got)
	}
}
