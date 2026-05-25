package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

func newEditModel() Model {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	m := New(c, ram)
	return m
}

func TestMemEdit_CommitTwoChar(t *testing.T) {
	m := newEditModel()
	m.MemCursor = 0x0200
	m.MemEditing = true

	if r := m.handleMemEditKey("4"); r != memEditEditing {
		t.Fatalf("after '4' want editing, got %v", r)
	}
	if r := m.handleMemEditKey("2"); r != memEditEditing {
		t.Fatalf("after '2' want editing, got %v", r)
	}
	if m.MemEditBuf != "42" {
		t.Fatalf("buffer want %q, got %q", "42", m.MemEditBuf)
	}
	if r := m.handleMemEditKey("enter"); r != memEditCommitted {
		t.Fatalf("after enter want committed, got %v", r)
	}
	if got := m.RAM.Read(0x0200); got != 0x42 {
		t.Fatalf("RAM[$0200] want $42, got $%02X", got)
	}
	if m.MemEditing {
		t.Fatalf("editing flag should be cleared after commit")
	}
	if m.MemEditBuf != "" {
		t.Fatalf("buffer should be cleared after commit, got %q", m.MemEditBuf)
	}
}

func TestMemEdit_CommitSingleChar(t *testing.T) {
	// One nibble = lower nibble; parseUint("a", 16) = 0x0A.
	m := newEditModel()
	m.MemCursor = 0x0205
	m.MemEditing = true

	m.handleMemEditKey("a")
	if r := m.handleMemEditKey("enter"); r != memEditCommitted {
		t.Fatalf("want committed, got %v", r)
	}
	if got := m.RAM.Read(0x0205); got != 0x0A {
		t.Fatalf("RAM[$0205] want $0A, got $%02X", got)
	}
}

func TestMemEdit_EnterEmptyCancels(t *testing.T) {
	m := newEditModel()
	m.MemCursor = 0x0210
	m.MemEditing = true
	m.RAM.Write(0x0210, 0x99)

	if r := m.handleMemEditKey("enter"); r != memEditCancelled {
		t.Fatalf("want cancelled, got %v", r)
	}
	if got := m.RAM.Read(0x0210); got != 0x99 {
		t.Fatalf("byte should be untouched: got $%02X", got)
	}
}

func TestMemEdit_Esc(t *testing.T) {
	m := newEditModel()
	m.MemCursor = 0x0211
	m.MemEditing = true
	m.RAM.Write(0x0211, 0x77)
	m.handleMemEditKey("3")
	m.handleMemEditKey("3")
	if r := m.handleMemEditKey("esc"); r != memEditCancelled {
		t.Fatalf("want cancelled, got %v", r)
	}
	if got := m.RAM.Read(0x0211); got != 0x77 {
		t.Fatalf("esc should discard buffer: got $%02X", got)
	}
	if m.MemEditing {
		t.Fatalf("editing should be off")
	}
}

func TestMemEdit_Backspace(t *testing.T) {
	m := newEditModel()
	m.MemEditing = true
	m.handleMemEditKey("a")
	m.handleMemEditKey("b")
	m.handleMemEditKey("backspace")
	if m.MemEditBuf != "a" {
		t.Fatalf("backspace want %q, got %q", "a", m.MemEditBuf)
	}
	m.handleMemEditKey("backspace")
	m.handleMemEditKey("backspace") // no-op on empty
	if m.MemEditBuf != "" {
		t.Fatalf("backspace-empty want %q, got %q", "", m.MemEditBuf)
	}
}

func TestMemEdit_RejectThirdChar(t *testing.T) {
	m := newEditModel()
	m.MemEditing = true
	m.handleMemEditKey("f")
	m.handleMemEditKey("f")
	m.handleMemEditKey("a") // should be ignored — buffer full
	if m.MemEditBuf != "ff" {
		t.Fatalf("third char must be rejected, got %q", m.MemEditBuf)
	}
}

func TestMemEdit_NonHexIgnored(t *testing.T) {
	m := newEditModel()
	m.MemEditing = true
	m.handleMemEditKey("z")
	m.handleMemEditKey("!")
	m.handleMemEditKey(" ")
	if m.MemEditBuf != "" {
		t.Fatalf("non-hex chars must be ignored, got %q", m.MemEditBuf)
	}
}

func TestMemCursorMoved_ReanchorsViewWhenOutOfRange(t *testing.T) {
	m := newEditModel()
	m.MemViewAddr = 0x0200
	m.MemCursor = 0x0500
	m.memCursorMoved()
	if m.MemViewAddr != 0x0500 {
		t.Fatalf("view should re-anchor to $0500, got $%04X", m.MemViewAddr)
	}
}

func TestMemCursorMoved_KeepsViewWhenInRange(t *testing.T) {
	m := newEditModel()
	m.MemViewAddr = 0x0200
	m.MemCursor = 0x0245
	m.memCursorMoved()
	if m.MemViewAddr != 0x0200 {
		t.Fatalf("view shouldn't move while cursor in range, got $%04X", m.MemViewAddr)
	}
}
