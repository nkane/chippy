package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nkane/chippy/internal/expr"
)

// ImmediateEntry is one row in the immediate-window scrollback. Result is
// pre-formatted (with the `$XX` width-by-magnitude shape `evaluate` uses)
// or an error string if the expression didn't compile / eval.
type ImmediateEntry struct {
	Expr   string `json:"expr"`
	Result string `json:"result"`
	Err    bool   `json:"err,omitempty"`
}

// immediateCap is the max number of scrollback rows kept in memory and
// rendered. Old entries fall off the top.
const immediateCap = 200

// updateImmediate owns input while the immediate window is open. Same
// key conventions as the `:` prompt: Esc closes, Enter evaluates,
// backspace pops, ctrl+c quits.
func (m Model) updateImmediate(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.ImmediateActive = false
		m.ImmediateBuf = ""
		m.Status = "immediate window closed"
		return m, m.scheduleTick()
	case "ctrl+c":
		m.saveState()
		return m, tea.Quit
	case "enter":
		expr := strings.TrimSpace(m.ImmediateBuf)
		m.ImmediateBuf = ""
		if expr == "" {
			return m, m.scheduleTick()
		}
		entry := m.evaluateImmediate(expr)
		m.ImmediateHistory = append(m.ImmediateHistory, entry)
		if len(m.ImmediateHistory) > immediateCap {
			m.ImmediateHistory = m.ImmediateHistory[len(m.ImmediateHistory)-immediateCap:]
		}
		return m, m.scheduleTick()
	case "backspace":
		if len(m.ImmediateBuf) > 0 {
			m.ImmediateBuf = m.ImmediateBuf[:len(m.ImmediateBuf)-1]
		}
		return m, m.scheduleTick()
	case "up":
		// Recall last expression into the buffer.
		if n := len(m.ImmediateHistory); n > 0 {
			m.ImmediateBuf = m.ImmediateHistory[n-1].Expr
		}
		return m, m.scheduleTick()
	}
	if len(msg.Runes) > 0 {
		m.ImmediateBuf += string(msg.Runes)
	}
	return m, m.scheduleTick()
}

// evaluateImmediate compiles + runs `src` against the current CPU state.
// Width-by-magnitude hex formatting matches the DAP `evaluate` response
// so the two surfaces show identical results for the same expression.
func (m *Model) evaluateImmediate(src string) ImmediateEntry {
	fn, err := expr.Compile(src, m.Syms)
	if err != nil {
		return ImmediateEntry{Expr: src, Result: err.Error(), Err: true}
	}
	v := fn(m.CPU, m.RAM)
	return ImmediateEntry{Expr: src, Result: formatImmediateResult(v)}
}

func formatImmediateResult(v uint32) string {
	switch {
	case v <= 0xFF:
		return fmt.Sprintf("$%02X  (%d)", v, v)
	case v <= 0xFFFF:
		return fmt.Sprintf("$%04X  (%d)", v, v)
	}
	return fmt.Sprintf("$%08X  (%d)", v, v)
}

// immediateModal renders the immediate-window overlay. Scrollback at top
// shows `expr → result` pairs, errors highlighted; current input line at
// bottom with a reversed cursor.
func (m Model) immediateModal() string {
	exprStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	resultStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Italic(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")

	const maxRows = 16
	hist := m.ImmediateHistory
	if len(hist) > maxRows {
		hist = hist[len(hist)-maxRows:]
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("immediate"))
	b.WriteString("\n\n")
	if len(hist) == 0 {
		b.WriteString(dim.Render("  expressions evaluate against current CPU state\n"))
		b.WriteString(dim.Render("  examples: A + X    [$0200]    PC >= main    A == $42\n"))
		b.WriteString(dim.Render("  ↑ recalls last; Esc closes\n"))
		b.WriteString("\n")
	}
	for _, e := range hist {
		line := fmt.Sprintf("  %s  →  ", exprStyle.Render(e.Expr))
		if e.Err {
			line += errStyle.Render(e.Result)
		} else {
			line += resultStyle.Render(e.Result)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	b.WriteString(exprStyle.Render("> ") + m.ImmediateBuf + cursor)

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("213")).
		Padding(1, 3).
		Render(b.String())
}
