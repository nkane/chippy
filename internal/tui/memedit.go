package tui

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

// memEditResult is what handleMemEditKey reports back to the Update loop.
//
//	committed: the user pressed Enter with a valid hex byte; the RAM write
//	           already happened. Status reflects the address + value.
//	cancelled: Esc, ctrl+c, or Enter on an empty buffer. No RAM change.
//	editing:   the key was consumed but we're still accumulating hex chars.
type memEditResult int

const (
	memEditEditing memEditResult = iota
	memEditCommitted
	memEditCancelled
	memEditQuit
)

// handleMemEditKey advances the memory editor's state machine. It owns
// MemEditBuf while editing and writes RAM on commit. Keeping this separate
// from updateMemEdit (which deals with the tea.KeyMsg + tea.Cmd dance)
// makes it directly unit-testable with plain strings.
func (m *Model) handleMemEditKey(s string) memEditResult {
	switch s {
	case "ctrl+c":
		m.MemEditing = false
		m.MemEditBuf = ""
		return memEditQuit
	case "esc":
		m.MemEditing = false
		m.MemEditBuf = ""
		m.Status = "edit cancelled"
		return memEditCancelled
	case "enter":
		if m.MemEditBuf == "" {
			m.MemEditing = false
			m.Status = "edit cancelled (empty)"
			return memEditCancelled
		}
		v, err := strconv.ParseUint(m.MemEditBuf, 16, 8)
		if err != nil {
			m.Status = fmt.Sprintf("bad hex: %s", m.MemEditBuf)
			m.MemEditing = false
			m.MemEditBuf = ""
			return memEditCancelled
		}
		m.RAM.Write(m.MemCursor, byte(v))
		m.Status = fmt.Sprintf("$%04X <- $%02X", m.MemCursor, byte(v))
		m.MemEditing = false
		m.MemEditBuf = ""
		return memEditCommitted
	case "backspace":
		if len(m.MemEditBuf) > 0 {
			m.MemEditBuf = m.MemEditBuf[:len(m.MemEditBuf)-1]
		}
		return memEditEditing
	}
	if len(s) == 1 && isHexDigit(s[0]) && len(m.MemEditBuf) < 2 {
		m.MemEditBuf += s
	}
	return memEditEditing
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'f') ||
		(b >= 'A' && b <= 'F')
}

// updateMemEdit owns input while the memory editor is active. Quit (ctrl+c)
// still flushes state via saveState/tracer.Close just like the normal quit
// path.
func (m Model) updateMemEdit(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.handleMemEditKey(msg.String()) {
	case memEditQuit:
		m.saveState()
		return m, tea.Quit
	default:
		return m, m.scheduleTick()
	}
}

// memCursorMoved snaps MemViewAddr to keep MemCursor visible. Approximates
// the visible window as 16 rows (0x100 bytes) — close enough that a
// fine-grained cursor doesn't disappear off-panel after a single arrow.
func (m *Model) memCursorMoved() {
	base := m.MemViewAddr & 0xFFF0
	const approxVisible = uint16(0x100)
	if m.MemCursor < base || m.MemCursor-base >= approxVisible {
		m.MemViewAddr = m.MemCursor & 0xFFF0
	}
}
