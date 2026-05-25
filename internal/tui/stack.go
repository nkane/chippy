package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nkane/chippy/cpu"
)

// stackEntry is one row in the annotated stack panel. A frame represents the
// two-byte JSR return-address pair pushed at addrLo / addrLo+1; a run is a
// collapsed group of consecutive non-frame bytes.
type stackEntry struct {
	isFrame bool
	addrLo  uint16
	bytes   int    // frame: always 2; run: byte count
	retAddr uint16 // frame only — cooked return address (stored + 1)
	callee  string // frame only — symbol name at JSR target, if known
	src     string // frame only — "file.s:NN" if a source map covers retAddr
}

// detectStackFrame is the TUI-side alias for cpu.DetectStackFrame; thin
// wrapper kept so existing call sites (and tests) don't grow extra import
// noise. The actual heuristic lives in the cpu package so the DAP server
// can share it without depending on tui.
func detectStackFrame(ram *cpu.RAM, spLo uint16) (retAddr, target uint16, ok bool) {
	return cpu.DetectStackFrame(ram, spLo)
}

// stackEntries walks upward from SP+1 building rendered rows for the stack
// panel. Frame rows consume two bytes; runs of non-frame bytes are
// collapsed into a single row per contiguous run. Stops at the panel-row
// budget or at the top of the stack page ($01FF).
func (m Model) stackEntries(maxRows int) []stackEntry {
	out := make([]stackEntry, 0, maxRows)
	a := uint16(m.CPU.SP) + 1
	runStart := uint16(0)
	runLen := 0
	flushRun := func() {
		if runLen == 0 {
			return
		}
		out = append(out, stackEntry{addrLo: runStart, bytes: runLen})
		runLen = 0
	}
	for a <= 0xFF && len(out) < maxRows {
		sp := uint16(0x0100) | a
		if m.StackAnnotate {
			if ret, target, ok := detectStackFrame(m.RAM, sp); ok && len(out) < maxRows {
				flushRun()
				if len(out) >= maxRows {
					break
				}
				e := stackEntry{
					isFrame: true,
					addrLo:  sp,
					bytes:   2,
					retAddr: ret,
				}
				if m.Syms != nil {
					e.callee = m.Syms.Lookup(target)
				}
				if loc, ok := m.PCToSrc[ret]; ok {
					e.src = fmt.Sprintf("%s:%d", filepath.Base(loc.File), loc.Line)
				}
				out = append(out, e)
				a += 2
				continue
			}
		}
		if runLen == 0 {
			runStart = sp
		}
		runLen++
		a++
	}
	flushRun()
	// flushRun may have pushed past maxRows by one — trim.
	if len(out) > maxRows {
		out = out[:maxRows]
	}
	return out
}

// stackView renders the Stack panel. When m.StackAnnotate is true (default),
// JSR return-address pairs are surfaced as `ret $XXXX  callee  file:NN`
// rows and adjacent non-frame bytes are collapsed. The `T` key toggles
// the flag back to the raw one-byte-per-row layout.
func (m Model) stackView(w, h int) string {
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	rows := innerH - 1
	if rows < 1 {
		rows = 1
	}

	var b strings.Builder

	if !m.StackAnnotate {
		for i := 0; i < rows; i++ {
			spByte := uint16(m.CPU.SP) + 1 + uint16(i)
			if spByte > 0xFF {
				break
			}
			sp := 0x100 | spByte
			marker := "  "
			if i == 0 {
				marker = curLine.Render(" >")
			}
			fmt.Fprintf(&b, "%s %s  %02X\n",
				marker,
				dimAddr.Render(fmt.Sprintf("$%04X", sp)),
				m.RAM.Read(sp))
		}
		return fitPanel("Stack", strings.TrimRight(b.String(), "\n"), w, h)
	}

	entries := m.stackEntries(rows)
	frameStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	runStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)

	for i, e := range entries {
		marker := "  "
		if i == 0 {
			marker = curLine.Render(" >")
		}
		switch {
		case e.isFrame:
			rng := dimAddr.Render(fmt.Sprintf("$%04X-%02X", e.addrLo, (e.addrLo+1)&0xFF))
			label := frameStyle.Render(fmt.Sprintf("ret $%04X", e.retAddr))
			if e.callee != "" {
				label += "  " + labelStyle.Render(e.callee)
			}
			if e.src != "" {
				label += dimAddr.Render("  " + e.src)
			}
			fmt.Fprintf(&b, "%s %s  %s\n", marker, rng, label)
		case e.bytes == 1:
			fmt.Fprintf(&b, "%s %s  %02X\n",
				marker,
				dimAddr.Render(fmt.Sprintf("$%04X", e.addrLo)),
				m.RAM.Read(e.addrLo))
		default:
			rng := dimAddr.Render(fmt.Sprintf("$%04X-%02X",
				e.addrLo,
				(e.addrLo+uint16(e.bytes)-1)&0xFF))
			fmt.Fprintf(&b, "%s %s  %s\n",
				marker, rng,
				runStyle.Render(fmt.Sprintf("(%d bytes)", e.bytes)))
		}
	}
	return fitPanel("Stack", strings.TrimRight(b.String(), "\n"), w, h)
}
