package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/nkane/chippy/internal/cpu"
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

// codeMinAddr is the lowest address we accept as plausibly executable code.
// $0000-$00FF is the zero page, $0100-$01FF is the stack page. Real programs
// don't put JSR opcodes or JSR targets in those regions, so requiring the
// stored return address AND the JSR target to be ≥ $0200 cuts most of the
// false-positive frames the bare opcode check produces. (Programs that DO
// abuse zero-page as code will lose frame annotation in the affected range
// but are degenerate cases — the panel still renders the bytes.)
const codeMinAddr uint16 = 0x0200

// detectStackFrame reports whether the byte pair at $01XX (spLo, spLo+1)
// looks like a JSR-pushed return address. The heuristic checks three
// signals; all must hold:
//
//  1. The byte two below the stored 16-bit value is the JSR opcode ($20).
//  2. The stored return address points at executable space (≥ codeMinAddr).
//  3. The JSR's call target (read from the two operand bytes that follow
//     the opcode) also points at executable space.
//
// retAddr is `stored + 1` (what RTS will jump to); target is the JSR's
// call target. Random byte pairs satisfying signal 1 alone get filtered
// out by 2 and 3 unless the noise happens to look like a real call into
// a real code region — exceedingly rare.
//
// False positives are still possible (misleading annotation, never a
// crash); `T` toggles the panel back to raw bytes when the heuristic
// misfires.
func detectStackFrame(ram *cpu.RAM, spLo uint16) (retAddr, target uint16, ok bool) {
	if (spLo & 0xFF) == 0xFF {
		return 0, 0, false
	}
	lo := ram.Read(spLo)
	hi := ram.Read(spLo + 1)
	stored := uint16(hi)<<8 | uint16(lo)
	if stored < codeMinAddr {
		return 0, 0, false
	}
	if ram.Read(stored-2) != 0x20 {
		return 0, 0, false
	}
	targetLo := ram.Read(stored - 1)
	targetHi := ram.Read(stored)
	target = uint16(targetHi)<<8 | uint16(targetLo)
	if target < codeMinAddr {
		return 0, 0, false
	}
	return stored + 1, target, true
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
