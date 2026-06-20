package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/symbols"
)

func TestStackEntries_FrameAndRun(t *testing.T) {
	// Build a stack with two frames separated by three non-frame bytes:
	//   $01FE-FF : frame ret $8003 (JSR at $8000)
	//   $01FD    : random byte
	//   $01FC    : random byte
	//   $01FB    : random byte
	//   $01F9-FA : frame ret $7003 (JSR at $7000)
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0x20)
	ram.Write(0x8001, 0x00)
	ram.Write(0x8002, 0x90)
	ram.Write(0x7000, 0x20)
	ram.Write(0x7001, 0x34)
	ram.Write(0x7002, 0x12)

	// Top frame at $01FE/FF -> stored=$8002
	ram.Write(0x01FE, 0x02)
	ram.Write(0x01FF, 0x80)
	// Random in-between bytes
	ram.Write(0x01FB, 0xAA)
	ram.Write(0x01FC, 0xBB)
	ram.Write(0x01FD, 0xCC)
	// Lower frame at $01F9/FA -> stored=$7002
	ram.Write(0x01F9, 0x02)
	ram.Write(0x01FA, 0x70)

	c := cpu.New(ram)
	c.SP = 0xF8 // SP+1 = $F9 → walk from $01F9

	m := New(c, ram)
	entries := m.stackEntries(10)

	if len(entries) != 3 {
		t.Fatalf("want 3 entries (frame, run-of-3, frame), got %d", len(entries))
	}
	if !entries[0].isFrame || entries[0].retAddr != 0x7003 {
		t.Fatalf("entry 0: want frame ret $7003, got %+v", entries[0])
	}
	if entries[1].isFrame || entries[1].bytes != 3 || entries[1].addrLo != 0x01FB {
		t.Fatalf("entry 1: want 3-byte run at $01FB, got %+v", entries[1])
	}
	if !entries[2].isFrame || entries[2].retAddr != 0x8003 {
		t.Fatalf("entry 2: want frame ret $8003, got %+v", entries[2])
	}
}

// TestStackEntries_SourceLineFromDAP proves the local-mode symbol wiring
// (issue #449): WithSourceMap pushes the source map into the in-process DAP
// server (SetSymbols), so a stackTrace frame's source line reaches the panel
// snapshot — the annotation no longer comes from a direct m.PCToSrc read.
func TestStackEntries_SourceLineFromDAP(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0x20) // JSR $9000
	ram.Write(0x8001, 0x00)
	ram.Write(0x8002, 0x90)
	ram.Write(0x01FE, 0x02) // stored $8002 -> ret $8003
	ram.Write(0x01FF, 0x80)

	c := cpu.New(ram)
	c.SP = 0xFD // SP+1 = $FE → walk from $01FE

	sm := &symbols.SourceMap{
		PCToSrc: map[uint16]symbols.SrcLoc{0x8003: {File: "main.s", Line: 42}},
	}
	m := New(c, ram).WithSourceMap(sm)

	entries := m.stackEntries(10)
	if len(entries) != 1 || !entries[0].isFrame {
		t.Fatalf("want 1 frame entry, got %+v", entries)
	}
	if entries[0].retAddr != 0x8003 {
		t.Fatalf("want ret $8003, got $%04X", entries[0].retAddr)
	}
	if entries[0].src != "main.s:42" {
		t.Fatalf("want src main.s:42 sourced via DAP, got %q", entries[0].src)
	}
}

func TestStackEntries_DisabledAnnotation(t *testing.T) {
	// With StackAnnotate=false the walker should collapse the entire visible
	// region into a single run (frame detection is skipped). Renderer takes
	// the legacy path; we still exercise stackEntries directly to confirm
	// the flag is honored.
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0x20)
	ram.Write(0x01FE, 0x02)
	ram.Write(0x01FF, 0x80)

	c := cpu.New(ram)
	c.SP = 0xFD

	m := New(c, ram)
	m.StackAnnotate = false
	entries := m.stackEntries(4)

	for i, e := range entries {
		if e.isFrame {
			t.Fatalf("entry %d should not be a frame when annotation is disabled: %+v", i, e)
		}
	}
}
