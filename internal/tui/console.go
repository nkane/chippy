package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Draft of issue #232 — the Quake-style drop-down console. Backtick
// (`) opens; Esc / backtick close. While open the console owns
// input — typing builds ConsoleBuf, Enter runs it via the existing
// runCommand path (same verbs as the `:` prompt), output streams
// into the scrollback. PgUp / PgDn walk the scrollback so output
// past the visible height isn't lost.
//
// This is the bones-only first cut. Polish items deferred to
// follow-ups: history walk inside the console, tab completion,
// scrollback persistence, transparent overlay (real terminal
// alpha doesn't exist; dim styling is the closest approximation).

const (
	consoleHeightRatio   = 2 // 1/2 of the screen
	consoleScrollbackMax = 500
)

// updateConsole handles all input while the console is open.
func (m Model) updateConsole(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "`":
		m.ConsoleActive = false
		m.ConsoleBuf = ""
		m.Status = "ready"
	case "ctrl+c":
		return m, tea.Quit
	case "enter":
		line := strings.TrimSpace(m.ConsoleBuf)
		m.ConsoleBuf = ""
		if line == "" {
			break
		}
		m.appendConsole("> " + line)
		out := m.runCommand(line)
		if out != "" {
			for _, ln := range strings.Split(out, "\n") {
				m.appendConsole(ln)
			}
		}
		// Snap back to the bottom so the freshly-typed command's
		// output is visible.
		m.ConsoleScrollOffset = 0
	case "backspace":
		if len(m.ConsoleBuf) > 0 {
			m.ConsoleBuf = m.ConsoleBuf[:len(m.ConsoleBuf)-1]
		}
	case "pgup":
		m.ConsoleScrollOffset += 8
		if max := len(m.ConsoleScrollback); m.ConsoleScrollOffset > max {
			m.ConsoleScrollOffset = max
		}
	case "pgdown":
		m.ConsoleScrollOffset -= 8
		if m.ConsoleScrollOffset < 0 {
			m.ConsoleScrollOffset = 0
		}
	default:
		// Plain printable runes append to the buffer. Skip the
		// backtick (already handled above as toggle) and any
		// non-printable control chars.
		if len(msg.Runes) == 1 {
			r := msg.Runes[0]
			if r >= 0x20 && r < 0x7F && r != '`' {
				m.ConsoleBuf += string(r)
			}
		}
	}
	return m, m.scheduleTick()
}

// appendConsole pushes one line onto the scrollback with a soft
// cap so long sessions don't grow unbounded.
func (m *Model) appendConsole(line string) {
	m.ConsoleScrollback = append(m.ConsoleScrollback, line)
	if over := len(m.ConsoleScrollback) - consoleScrollbackMax; over > 0 {
		m.ConsoleScrollback = m.ConsoleScrollback[over:]
	}
}

// consoleView renders the drop-down overlay. Sized to ~half the
// screen height; bordered + headed; bottom row is the live input
// line with a `>` prompt.
func (m Model) consoleView(width, totalHeight int) string {
	height := totalHeight / consoleHeightRatio
	if height < 6 {
		height = 6
	}
	if height > totalHeight {
		height = totalHeight
	}

	header := lipgloss.NewStyle().
		Foreground(lipgloss.Color("207")).
		Bold(true).
		Render(" chippy console — esc/` close · PgUp/PgDn scroll ")
	frame := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("213")).
		Padding(0, 1)
	prompt := lipgloss.NewStyle().Foreground(lipgloss.Color("213"))

	// Reserve: 1 header + 2 border + 1 input = 4 rows of chrome.
	scrollRows := height - 4
	if scrollRows < 1 {
		scrollRows = 1
	}

	// Bottom-anchored scrollback with PgUp/PgDn offset.
	total := len(m.ConsoleScrollback)
	end := total - m.ConsoleScrollOffset
	if end < 0 {
		end = 0
	}
	if end > total {
		end = total
	}
	start := end - scrollRows
	if start < 0 {
		start = 0
	}
	visible := m.ConsoleScrollback[start:end]

	var body strings.Builder
	body.WriteString(header)
	body.WriteString("\n")
	// Pad scrollback area to scrollRows so the input line stays
	// pinned at the same y.
	for i := 0; i < scrollRows-len(visible); i++ {
		body.WriteString("\n")
	}
	for _, ln := range visible {
		body.WriteString(ln + "\n")
	}
	body.WriteString(prompt.Render("> ") + m.ConsoleBuf + "_")

	return frame.Width(width - 4).Render(body.String())
}
