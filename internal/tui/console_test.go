package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nkane/chippy/internal/cpu"
)

func newConsoleModel(t *testing.T) Model {
	t.Helper()
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	return New(c, ram)
}

// Backtick from the main key handler opens the console.
func TestConsole_BacktickOpens(t *testing.T) {
	m := newConsoleModel(t)
	if m.ConsoleActive {
		t.Fatalf("ConsoleActive should default false")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'`'}})
	m = updated.(Model)
	if !m.ConsoleActive {
		t.Errorf("backtick didn't toggle ConsoleActive on")
	}
	if m.ConsoleBuf != "" {
		t.Errorf("ConsoleBuf should reset on open; got %q", m.ConsoleBuf)
	}
}

// Esc from inside the console closes it.
func TestConsole_EscCloses(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	m.ConsoleBuf = "stale"
	updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.ConsoleActive {
		t.Errorf("esc didn't close console")
	}
	if m.ConsoleBuf != "" {
		t.Errorf("esc should clear buf; got %q", m.ConsoleBuf)
	}
}

// Backtick from inside the console also closes it (toggle).
func TestConsole_BacktickInsideCloses(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'`'}})
	m = updated.(Model)
	if m.ConsoleActive {
		t.Errorf("backtick inside console didn't close it")
	}
}

// Typing printable characters appends to the buffer. Backtick is
// the toggle, NOT a filterable character — typing it closes the
// console (verified separately in TestConsole_BacktickInsideCloses).
func TestConsole_TypingAppendsBuf(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	for _, r := range []rune{'h', 'e', 'l', 'l', 'o'} {
		updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}
	if m.ConsoleBuf != "hello" {
		t.Errorf("buf = %q; want %q", m.ConsoleBuf, "hello")
	}
}

// Enter runs the buffer through runCommand and appends `> cmd` plus
// the result to the scrollback.
func TestConsole_EnterRunsAndAppendsScrollback(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	m.ConsoleBuf = "pc"
	updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if len(m.ConsoleScrollback) == 0 {
		t.Fatalf("scrollback empty after Enter")
	}
	if m.ConsoleScrollback[0] != "> pc" {
		t.Errorf("scrollback[0] = %q; want %q", m.ConsoleScrollback[0], "> pc")
	}
	if m.ConsoleBuf != "" {
		t.Errorf("buf not cleared after Enter; got %q", m.ConsoleBuf)
	}
}

// Empty Enter is a no-op — no scrollback growth, no error.
func TestConsole_EmptyEnterIsNoop(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	pre := len(m.ConsoleScrollback)
	updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if len(m.ConsoleScrollback) != pre {
		t.Errorf("empty Enter grew scrollback by %d", len(m.ConsoleScrollback)-pre)
	}
}

// Backspace shortens the buffer; doesn't underflow.
func TestConsole_Backspace(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	m.ConsoleBuf = "abc"
	updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	if m.ConsoleBuf != "ab" {
		t.Errorf("backspace → %q; want %q", m.ConsoleBuf, "ab")
	}
	// Empty-buf backspace shouldn't panic / underflow.
	m.ConsoleBuf = ""
	updated, _ = m.updateConsole(tea.KeyMsg{Type: tea.KeyBackspace})
	m = updated.(Model)
	if m.ConsoleBuf != "" {
		t.Errorf("backspace on empty buf changed it to %q", m.ConsoleBuf)
	}
}

// PgUp grows the scroll offset; PgDn shrinks; both clamp.
func TestConsole_ScrollClamps(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	for i := 0; i < 5; i++ {
		m.ConsoleScrollback = append(m.ConsoleScrollback, "line")
	}
	// PgUp from offset 0 → up to 5 (len cap).
	for range 10 {
		updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyPgUp})
		m = updated.(Model)
	}
	if m.ConsoleScrollOffset != len(m.ConsoleScrollback) {
		t.Errorf("PgUp clamp = %d; want %d", m.ConsoleScrollOffset, len(m.ConsoleScrollback))
	}
	// PgDn floors at 0.
	for range 10 {
		updated, _ := m.updateConsole(tea.KeyMsg{Type: tea.KeyPgDown})
		m = updated.(Model)
	}
	if m.ConsoleScrollOffset != 0 {
		t.Errorf("PgDn floor = %d; want 0", m.ConsoleScrollOffset)
	}
}

// consoleView renders a header + the live input line at minimum,
// even with empty scrollback. Smoke test against the rendered
// string.
func TestConsole_ViewRenders(t *testing.T) {
	m := newConsoleModel(t)
	m.ConsoleActive = true
	m.ConsoleBuf = "hello"
	out := m.consoleView(120, 40)
	if !strings.Contains(out, "console") {
		t.Errorf("header missing in view:\n%s", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("buf %q missing from view:\n%s", m.ConsoleBuf, out)
	}
}
