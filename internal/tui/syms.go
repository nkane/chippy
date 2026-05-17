package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// symEntry is one row in the symbols modal: a name + the address it
// resolves to. Sorted by address (lowest first) so the list mirrors
// how the program is laid out in memory; ties broken on name.
type symEntry struct {
	name string
	addr uint16
}

// collectSymbols snapshots the loaded `.dbg` symbol table into a
// sorted slice the modal can paginate. Applies the optional substring
// filter case-insensitively against the symbol name. Returns nil if
// no symbols are loaded.
func (m *Model) collectSymbols() []symEntry {
	if m.Syms == nil || !m.Syms.Has() {
		return nil
	}
	filter := strings.ToLower(strings.TrimSpace(m.SymsFilter))
	names := m.Syms.NamesWithPrefix("")
	out := make([]symEntry, 0, len(names))
	for _, n := range names {
		if filter != "" && !strings.Contains(strings.ToLower(n), filter) {
			continue
		}
		if addr, ok := m.Syms.LookupName(n); ok {
			out = append(out, symEntry{name: n, addr: addr})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].addr != out[j].addr {
			return out[i].addr < out[j].addr
		}
		return out[i].name < out[j].name
	})
	return out
}

// updateSymsManager handles input while the symbols modal is open.
// Esc / q closes; j / k navigate; Enter toggles a breakpoint at the
// highlighted address. `/` enters filter-edit mode, backspace clears
// it character by character.
func (m Model) updateSymsManager(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	entries := m.collectSymbols()
	switch msg.String() {
	case "esc", "q":
		m.ShowSyms = false
		m.SymsFilter = ""
		m.SymsCursor = 0
		m.SymsOffset = 0
		m.Status = "ready"
	case "j", "down":
		if m.SymsCursor < len(entries)-1 {
			m.SymsCursor++
		}
	case "k", "up":
		if m.SymsCursor > 0 {
			m.SymsCursor--
		}
	case "g", "home":
		m.SymsCursor = 0
	case "G", "end":
		m.SymsCursor = len(entries) - 1
		if m.SymsCursor < 0 {
			m.SymsCursor = 0
		}
	case "enter":
		if m.SymsCursor < len(entries) {
			addr := entries[m.SymsCursor].addr
			if _, ok := m.Breakpoints[addr]; ok {
				delete(m.Breakpoints, addr)
				m.Status = fmt.Sprintf("bp -$%04X %s", addr, entries[m.SymsCursor].name)
			} else {
				bp := newBP(addr)
				m.Breakpoints[addr] = bp
				m.Status = fmt.Sprintf("bp +$%04X %s", addr, entries[m.SymsCursor].name)
			}
			m.syncSourceBreakpoints()
			m.saveState()
		}
	case "backspace":
		if len(m.SymsFilter) > 0 {
			m.SymsFilter = m.SymsFilter[:len(m.SymsFilter)-1]
			m.SymsCursor = 0
		}
	case "ctrl+c":
		return m, tea.Quit
	default:
		// Plain-character keys append to the filter.
		if len(msg.Runes) == 1 && msg.Runes[0] >= 0x20 && msg.Runes[0] < 0x7F {
			r := msg.Runes[0]
			// Skip "vi"-style navigation chars that already have
			// dedicated handlers above; appending them to the filter
			// would be surprising.
			if r != 'j' && r != 'k' && r != 'g' && r != 'G' && r != 'q' {
				m.SymsFilter += string(r)
				m.SymsCursor = 0
			}
		}
	}
	return m, m.scheduleTick()
}

// symsModal renders the symbols list. Pagination is automatic — the
// cursor stays visible as the user navigates, with header info
// showing the total count + filter state.
func (m Model) symsModal() string {
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("207")).Bold(true).Underline(true)
	footerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)
	selStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	addrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("108"))
	frame := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("213")).
		Padding(1, 3)

	if m.Syms == nil || !m.Syms.Has() {
		body := headerStyle.Render("Symbols") + "\n\n" +
			"No symbols loaded.\n\n" +
			"Pass -dbg PATH on launch, or rebuild the ROM with\n" +
			"`ca65 -g` + `ld65 --dbgfile` so a sibling .dbg ships\n" +
			"alongside the .nes.\n\n" +
			footerStyle.Render("esc/q close")
		return frame.Render(body)
	}

	entries := m.collectSymbols()
	rows := 16
	totalRows := len(entries)
	if m.SymsCursor >= totalRows {
		m.SymsCursor = totalRows - 1
		if m.SymsCursor < 0 {
			m.SymsCursor = 0
		}
	}
	start := m.SymsCursor - rows/2
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > totalRows {
		end = totalRows
		start = end - rows
		if start < 0 {
			start = 0
		}
	}

	var b strings.Builder
	header := fmt.Sprintf("Symbols (%d", totalRows)
	if m.SymsFilter != "" {
		header += fmt.Sprintf(" matching %q", m.SymsFilter)
	}
	header += ")"
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n\n")

	if totalRows == 0 {
		b.WriteString("  (no matches)\n\n")
	} else {
		for i := start; i < end; i++ {
			e := entries[i]
			marker := "  "
			if i == m.SymsCursor {
				marker = "→ "
			}
			bpMarker := " "
			if _, ok := m.Breakpoints[e.addr]; ok {
				bpMarker = "●"
			}
			line := fmt.Sprintf("%s%s %s  %s", marker, bpMarker,
				addrStyle.Render(fmt.Sprintf("$%04X", e.addr)), e.name)
			if i == m.SymsCursor {
				line = selStyle.Render(line)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	b.WriteString(footerStyle.Render("enter: toggle bp · j/k: scroll · g/G: first/last · type: filter · esc/q: close"))
	return frame.Render(b.String())
}
