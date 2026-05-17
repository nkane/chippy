package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/symbols"
)

// buildModelWithSyms constructs a Model with a handful of fake
// symbols so the modal has something to render. The exact addresses
// don't matter — only the count + name ordering.
func buildModelWithSyms(t *testing.T) Model {
	t.Helper()
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	m := New(c, ram)
	// Use the real hello-bg .dbg if available; it has the labels we
	// want to exercise. Skip if not (e.g. running on a worktree
	// without the demo artifacts).
	tbl, err := symbols.LoadDbg("../../roms/demos/hello-bg/hello-bg.dbg")
	if err != nil {
		t.Skipf("hello-bg.dbg not present: %v", err)
	}
	m.Syms = tbl
	return m
}

// collectSymbols sorts by address; first entry should be the lowest.
// The hello-bg demo's reset label sits at $C000.
func TestCollectSymbols_SortedByAddr(t *testing.T) {
	m := buildModelWithSyms(t)
	entries := m.collectSymbols()
	if len(entries) == 0 {
		t.Fatalf("collectSymbols returned empty list")
	}
	if entries[0].addr != 0xC000 || entries[0].name != "reset" {
		t.Errorf("entries[0] = ($%04X, %s); want ($C000, reset)", entries[0].addr, entries[0].name)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].addr < entries[i-1].addr {
			t.Errorf("entries not sorted at %d: $%04X < $%04X", i, entries[i].addr, entries[i-1].addr)
		}
	}
}

// Filter narrows the visible set to entries containing the
// substring (case-insensitive).
func TestCollectSymbols_FilterSubstring(t *testing.T) {
	m := buildModelWithSyms(t)
	m.SymsFilter = "clear"
	entries := m.collectSymbols()
	for _, e := range entries {
		if !strings.Contains(strings.ToLower(e.name), "clear") {
			t.Errorf("filter %q allowed %q to slip through", m.SymsFilter, e.name)
		}
	}
	if len(entries) == 0 {
		t.Errorf("filter clear should match clear_ram + clear_nt; got 0 entries")
	}
}

// Enter on a row toggles a breakpoint at the highlighted address.
func TestSymsModal_EnterTogglesBreakpoint(t *testing.T) {
	m := buildModelWithSyms(t)
	m.ShowSyms = true
	entries := m.collectSymbols()
	if len(entries) == 0 {
		t.Skip("no symbols loaded")
	}
	target := entries[0].addr

	// Initially no bp.
	if _, ok := m.Breakpoints[target]; ok {
		t.Fatalf("pre-test: bp already exists at $%04X", target)
	}

	// Press enter — should toggle ON.
	updated, _ := m.updateSymsManager(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if _, ok := m.Breakpoints[target]; !ok {
		t.Errorf("enter should have created bp at $%04X", target)
	}

	// Press enter again — should toggle OFF.
	updated, _ = m.updateSymsManager(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if _, ok := m.Breakpoints[target]; ok {
		t.Errorf("second enter should have deleted bp at $%04X", target)
	}
}

// Esc / q closes the modal and clears filter state.
func TestSymsModal_EscClosesAndClearsState(t *testing.T) {
	m := buildModelWithSyms(t)
	m.ShowSyms = true
	m.SymsFilter = "clear"
	m.SymsCursor = 3
	updated, _ := m.updateSymsManager(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.ShowSyms {
		t.Errorf("ShowSyms still true after esc")
	}
	if m.SymsFilter != "" {
		t.Errorf("SymsFilter = %q; want empty", m.SymsFilter)
	}
	if m.SymsCursor != 0 {
		t.Errorf("SymsCursor = %d; want 0", m.SymsCursor)
	}
}
