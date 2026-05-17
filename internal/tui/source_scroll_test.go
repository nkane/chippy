package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/symbols"
)

// buildModelWithSource constructs a Model wired up with a fake source
// file + PC-to-source map so scroll behavior can be exercised without
// requiring a real .dbg on disk.
func buildModelWithSource(t *testing.T) Model {
	t.Helper()
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	m := New(c, ram)
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "; line " + strings.Repeat("x", i+1)
	}
	m.SourceFiles = map[string][]string{"fake.s": lines}
	m.PCToSrc = map[uint16]symbols.SrcLoc{
		0xC000: {File: "fake.s", Line: 10},
	}
	c.PC = 0xC000
	m.ShowSource = true
	return m
}

// First scroll bootstraps anchor from the PC's source line and flips
// SourceFollow off.
func TestSourceScroll_FirstScrollPins(t *testing.T) {
	m := buildModelWithSource(t)
	if !m.SourceFollow {
		t.Fatalf("pre-test: expected SourceFollow=true by default")
	}
	m.sourceScroll(+3)
	if m.SourceFollow {
		t.Errorf("SourceFollow should be false after first scroll")
	}
	if m.SourceAnchorFile != "fake.s" {
		t.Errorf("SourceAnchorFile = %q; want fake.s", m.SourceAnchorFile)
	}
	if m.SourceAnchorLine != 13 {
		t.Errorf("SourceAnchorLine = %d; want 13 (10 + 3)", m.SourceAnchorLine)
	}
}

// Subsequent scrolls move from the anchor, not from PC.
func TestSourceScroll_StepsByDelta(t *testing.T) {
	m := buildModelWithSource(t)
	m.sourceScroll(+5) // pins to line 15
	m.sourceScroll(+5) // → 20
	m.sourceScroll(-3) // → 17
	if m.SourceAnchorLine != 17 {
		t.Errorf("SourceAnchorLine = %d; want 17", m.SourceAnchorLine)
	}
}

// Scrolling past the end clamps to len(lines); past start clamps to 1.
func TestSourceScroll_Clamps(t *testing.T) {
	m := buildModelWithSource(t)
	m.sourceScroll(+10000)
	if m.SourceAnchorLine != 50 {
		t.Errorf("upper clamp = %d; want 50", m.SourceAnchorLine)
	}
	m.sourceScroll(-10000)
	if m.SourceAnchorLine != 1 {
		t.Errorf("lower clamp = %d; want 1", m.SourceAnchorLine)
	}
}

// sourceView in pinned mode centers on the anchor, not PC. We render a
// small viewport and verify the anchor line appears in the output.
func TestSourceView_PinnedShowsAnchor(t *testing.T) {
	m := buildModelWithSource(t)
	m.sourceScroll(+30) // pin to line 40, far from PC's line 10
	out := m.sourceView(80, 12)
	if !strings.Contains(out, "pinned") {
		t.Errorf("pinned title hint missing in:\n%s", out)
	}
	if !strings.Contains(out, "fake.s:40") {
		t.Errorf("anchor line not in title:\n%s", out)
	}
}

// `'` restores follow mode: setting SourceFollow back true makes
// sourceView re-center on PC, dropping the pinned title.
func TestSourceView_FollowRestores(t *testing.T) {
	m := buildModelWithSource(t)
	m.sourceScroll(+5)
	m.SourceFollow = true
	out := m.sourceView(80, 12)
	if strings.Contains(out, "pinned") {
		t.Errorf("pinned hint should be gone after follow restored:\n%s", out)
	}
	if !strings.Contains(out, "fake.s:10") {
		t.Errorf("follow mode should center on PC line 10:\n%s", out)
	}
}
