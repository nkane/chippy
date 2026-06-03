package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

func newWatchModel() Model {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	return New(c, ram)
}

func lastWatch(m Model) Watch {
	return m.Watches[len(m.Watches)-1]
}

func TestParseCountToken(t *testing.T) {
	cases := []struct {
		in string
		n  int
		ok bool
	}{
		{"x4", 4, true},
		{"X16", 16, true},
		{"[8]", 8, true},
		{"x0", 0, false}, // count must be >= 1
		{"x", 0, false},  // no number
		{"[abc]", 0, false},
		{"word", 0, false}, // plain label/kind word
		{"4", 0, false},    // bare number is a label, not a count
	}
	for _, c := range cases {
		n, ok := parseCountToken(c.in)
		if n != c.n || ok != c.ok {
			t.Errorf("parseCountToken(%q) = (%d,%v); want (%d,%v)", c.in, n, ok, c.n, c.ok)
		}
	}
}

func TestWatchArray_xN(t *testing.T) {
	m := newWatchModel()
	out := m.runCommand("watch $0400 word x4")
	if !strings.Contains(out, "[4]") {
		t.Errorf("status %q missing [4]", out)
	}
	w := lastWatch(m)
	if w.Count != 4 || w.Width != 2 || w.Addr != 0x0400 {
		t.Fatalf("watch = %+v; want Count=4 Width=2 Addr=$0400", w)
	}
}

func TestWatchArray_BracketAndLabel(t *testing.T) {
	m := newWatchModel()
	m.runCommand("watch $0600 [8] sprite")
	w := lastWatch(m)
	if w.Count != 8 || w.Width != 1 {
		t.Fatalf("watch = %+v; want Count=8 Width=1", w)
	}
	if w.Label != "sprite" {
		t.Errorf("label = %q; want sprite", w.Label)
	}
}

func TestWatchArray_CountCapped(t *testing.T) {
	m := newWatchModel()
	m.runCommand("watch $0200 x9999")
	if w := lastWatch(m); w.Count != maxWatchCount {
		t.Errorf("Count = %d; want capped %d", w.Count, maxWatchCount)
	}
}

func TestWatchArray_Render(t *testing.T) {
	m := newWatchModel()
	m.RAM.Write(0x0400, 0xAA)
	m.RAM.Write(0x0401, 0xBB)
	m.runCommand("watch $0400 x2 buf")
	view := m.watchView(40, 12)
	for _, want := range []string{"buf", "[2]", "[0]", "[1]", "AA", "BB"} {
		if !strings.Contains(view, want) {
			t.Errorf("watchView missing %q:\n%s", want, view)
		}
	}
}

func TestWatchArray_RenderTruncates(t *testing.T) {
	m := newWatchModel()
	m.runCommand("watch $0200 x20 big")
	view := m.watchView(40, 12)
	if !strings.Contains(view, "more") {
		t.Errorf("expected truncation marker, got:\n%s", view)
	}
}

func TestWatchScalarUnaffected(t *testing.T) {
	m := newWatchModel()
	m.RAM.Write(0x0010, 0x7F)
	m.runCommand("watch $0010")
	if w := lastWatch(m); w.Count != 0 {
		t.Errorf("scalar watch Count = %d; want 0", w.Count)
	}
	if view := m.watchView(40, 6); !strings.Contains(view, "7F") {
		t.Errorf("scalar render missing value:\n%s", view)
	}
}
