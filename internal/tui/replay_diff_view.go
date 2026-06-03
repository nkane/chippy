package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/nkane/chippy/trace"
)

// diffRowStyles are the lipgloss styles for the side-by-side diff rows.
var (
	diffMatch  = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	diffMiss   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	diffCursor = lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	diffHdr    = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	diffGutter = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// diffModal renders the `-diff` side-by-side view: a window of frames from
// both traces centred on the primary cursor, left column = -trace-replay,
// right column = -diff. Frames that disagree are red; the cursor row is
// bright; the divergence frame is flagged in the header and a ✗ gutter.
func (m Model) diffModal(maxRows int) string {
	if m.ReplayDiff == nil || m.TraceReplay == nil {
		return "diff: no second trace loaded"
	}
	var b strings.Builder

	if m.Diverge.Found {
		b.WriteString(diffHdr.Render(fmt.Sprintf(
			"DIVERGE @ CYC:%d  (frame %d/%d)", m.Diverge.Cycle, m.Diverge.Index+1, m.TraceReplay.Len())))
	} else {
		b.WriteString(diffHdr.Render("traces identical over their overlap"))
	}
	b.WriteByte('\n')
	b.WriteString(diffGutter.Render(fmt.Sprintf("   %-46s %s",
		fmt.Sprintf("L: -trace-replay (%s)", m.replayLabel(m.TraceReplay)),
		fmt.Sprintf("R: -diff (%s)", m.replayLabel(m.ReplayDiff)))))
	b.WriteString("\n\n")

	// Window of rows centred on the primary cursor, clamped to trace bounds.
	rows := maxRows - 6
	if rows < 6 {
		rows = 6
	}
	if rows > 40 {
		rows = 40
	}
	anchor := m.TraceReplay.Index
	half := rows / 2
	start := anchor - half
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > m.TraceReplay.Len() {
		end = m.TraceReplay.Len()
	}

	for i := start; i < end; i++ {
		lf, lok := frameAt(m.TraceReplay, i)
		rf, rok := frameAt(m.ReplayDiff, i)
		mismatch := lok != rok || (lok && rok && !lf.Equal(rf))

		gutter := "  "
		switch {
		case m.Diverge.Found && i == m.Diverge.Index:
			gutter = diffMiss.Render("✗ ")
		case i == anchor:
			gutter = diffCursor.Render("▶ ")
		}

		style := diffMatch
		if mismatch {
			style = diffMiss
		} else if i == anchor {
			style = diffCursor
		}

		lcol := style.Render(fmt.Sprintf("%-46s", fmtDiffFrame(lf, lok)))
		rcol := style.Render(fmtDiffFrame(rf, rok))
		fmt.Fprintf(&b, "%s%s %s\n", gutter, lcol, rcol)
	}
	b.WriteString(diffGutter.Render("\n[d] close  [D] jump to divergence  [s/<] step"))
	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("39")).
		Padding(1, 2).
		Render(b.String())
}

// frameAt returns the frame at index i, or (zero, false) when out of range.
func frameAt(r *trace.Replay, i int) (trace.Frame, bool) {
	if r == nil || i < 0 || i >= r.Len() {
		return trace.Frame{}, false
	}
	return r.Frames[i], true
}

// fmtDiffFrame is the compact one-line frame rendering used in both diff
// columns. Missing frames (one trace shorter than the other) render as "—".
func fmtDiffFrame(f trace.Frame, ok bool) string {
	if !ok {
		return "—"
	}
	return fmt.Sprintf("PC:%04X A:%02X X:%02X Y:%02X P:%02X SP:%02X CYC:%d",
		f.PC, f.A, f.X, f.Y, f.P, f.SP, f.Cycles)
}

// replayLabel is a short descriptor for a replay (frame count) used in the
// diff header.
func (m Model) replayLabel(r *trace.Replay) string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("%d frames", r.Len())
}
