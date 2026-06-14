package tui

import (
	"strings"
	"testing"
)

func TestParseStructSpec(t *testing.T) {
	fields, err := parseStructSpec("{hp:byte, x:word, y:word}")
	if err != nil {
		t.Fatalf("parseStructSpec error: %v", err)
	}
	want := []WatchField{
		{Name: "hp", Offset: 0, Width: 1},
		{Name: "x", Offset: 1, Width: 2},
		{Name: "y", Offset: 3, Width: 2},
	}
	if len(fields) != len(want) {
		t.Fatalf("got %d fields, want %d: %+v", len(fields), len(want), fields)
	}
	for i, w := range want {
		if fields[i] != w {
			t.Errorf("field[%d] = %+v; want %+v", i, fields[i], w)
		}
	}
}

func TestParseStructSpec_ExplicitOffset(t *testing.T) {
	// An explicit @N overrides the running cursor; auto-advance resumes from it.
	fields, err := parseStructSpec("{a:byte, b@8:word, c:byte}")
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	want := []WatchField{
		{Name: "a", Offset: 0, Width: 1},
		{Name: "b", Offset: 8, Width: 2},
		{Name: "c", Offset: 10, Width: 1},
	}
	for i, w := range want {
		if fields[i] != w {
			t.Errorf("field[%d] = %+v; want %+v", i, fields[i], w)
		}
	}
}

func TestParseStructSpec_Errors(t *testing.T) {
	for _, in := range []string{
		"hp:byte",      // no braces
		"{}",           // empty
		"{hp}",         // no width
		"{hp:dword}",   // bad width
		"{:byte}",      // missing name
		"{hp@xx:byte}", // bad offset
	} {
		if _, err := parseStructSpec(in); err == nil {
			t.Errorf("parseStructSpec(%q) expected error, got nil", in)
		}
	}
}

func TestWatchStruct_Command(t *testing.T) {
	m := newWatchModel()
	out := m.runCommand("watch $0400 player as {hp:byte, x:word, y:word}")
	if !strings.Contains(out, "{3}") {
		t.Errorf("status %q missing {3}", out)
	}
	w := lastWatch(m)
	if w.Addr != 0x0400 || len(w.Fields) != 3 || w.Label != "player" {
		t.Fatalf("watch = %+v; want Addr=$0400 3 fields label=player", w)
	}
}

func TestWatchStruct_Render(t *testing.T) {
	m := newWatchModel()
	m.RAM.Write(0x0400, 0x2A) // hp = $2A
	m.RAM.Write(0x0401, 0x34) // x lo
	m.RAM.Write(0x0402, 0x12) // x hi -> $1234
	m.RAM.Write(0x0403, 0x78) // y lo
	m.RAM.Write(0x0404, 0x56) // y hi -> $5678
	m.runCommand("watch $0400 player as {hp:byte, x:word, y:word}")
	view := m.watchView(40, 12)
	for _, want := range []string{"player", "{3}", "hp", "2A", "x", "1234", "y", "5678"} {
		if !strings.Contains(view, want) {
			t.Errorf("watchView missing %q:\n%s", want, view)
		}
	}
}

func TestWatchStruct_RowCount(t *testing.T) {
	m := newWatchModel()
	m.runCommand("watch $0400 as {hp:byte, x:word, y:word}")
	if got := m.watchRowCount(); got != 4 { // 1 header + 3 members
		t.Errorf("watchRowCount = %d; want 4", got)
	}
}

func TestWatchStruct_Remove(t *testing.T) {
	m := newWatchModel()
	m.runCommand("watch $0400 as {hp:byte}")
	m.runCommand("rmwatch $0400")
	if len(m.Watches) != 0 {
		t.Errorf("watch not removed: %+v", m.Watches)
	}
}
