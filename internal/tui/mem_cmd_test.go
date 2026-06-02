package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

func newMemCmdModel() Model {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	return New(c, ram)
}

func TestCmdMem_SingleByte(t *testing.T) {
	m := newMemCmdModel()
	got := m.cmdMem([]string{"$0200", "$42"})
	if !strings.Contains(got, "$0200") || !strings.Contains(got, "$42") {
		t.Fatalf("status: %q", got)
	}
	if v := m.RAM.Read(0x0200); v != 0x42 {
		t.Errorf("RAM[$0200] = $%02X; want $42", v)
	}
}

func TestCmdMem_MultiBytes(t *testing.T) {
	m := newMemCmdModel()
	if got := m.cmdMem([]string{"$0300", "41", "42", "43"}); !strings.Contains(got, "3 bytes") {
		t.Fatalf("status: %q", got)
	}
	for i, want := range []byte{41, 42, 43} {
		if v := m.RAM.Read(uint16(0x0300 + i)); v != want {
			t.Errorf("RAM[$%04X] = $%02X; want $%02X", 0x0300+i, v, want)
		}
	}
}

func TestCmdMem_HexValues(t *testing.T) {
	m := newMemCmdModel()
	m.cmdMem([]string{"$0400", "$FF", "0xAB", "127"})
	for i, want := range []byte{0xFF, 0xAB, 127} {
		if v := m.RAM.Read(uint16(0x0400 + i)); v != want {
			t.Errorf("RAM[$%04X] = $%02X; want $%02X", 0x0400+i, v, want)
		}
	}
}

func TestCmdMem_BadByte(t *testing.T) {
	m := newMemCmdModel()
	got := m.cmdMem([]string{"$0500", "$100"}) // exceeds byte range
	if !strings.Contains(strings.ToLower(got), "byte range") {
		t.Errorf("expected byte-range error, got %q", got)
	}
	if v := m.RAM.Read(0x0500); v != 0x00 {
		t.Errorf("RAM[$0500] = $%02X; want 0 (no partial write on error)", v)
	}
}

func TestCmdMem_Usage(t *testing.T) {
	m := newMemCmdModel()
	got := m.cmdMem([]string{"$0600"}) // missing value
	if !strings.Contains(strings.ToLower(got), "usage") {
		t.Errorf("expected usage, got %q", got)
	}
}

func TestCmdMem_BadAddr(t *testing.T) {
	m := newMemCmdModel()
	got := m.cmdMem([]string{"NOSUCH", "$42"})
	if got == "" || strings.HasPrefix(got, "$") {
		t.Errorf("expected error string, got %q", got)
	}
}
