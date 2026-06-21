package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// StackSnapshot is the stack-page frame data the Stack panel renders, sourced
// via a DAP `stackTrace` round-trip (issue #449) — the panel no longer walks
// cpu.RAM with DetectStackFrame itself. Frames carry the stack-page slot,
// return address, callee symbol, and source line; Page holds the 256 raw
// stack-page bytes ($0100-$01FF) fetched via DAP `readMemory` so the panel's
// byte/run rows are DAP-sourced too (issue #461 — no direct m.RAM.Read in the
// render path). Local mode round-trips an in-process DAP server
// (sub-microsecond, #393); remote reuses the attach client.
type StackSnapshot struct {
	Frames []stackFrame
	Page   []byte // $0100-$01FF, indexed by (addr & 0xFF)
}

// stackFrame is one detected JSR return-address pair from the stackTrace
// response, in ascending stack-page order.
type stackFrame struct {
	addrLo  uint16 // low addr in the $0100 page where the pushed pair begins
	retAddr uint16 // cooked return address (stored + 1)
	callee  string // symbol at the JSR target, "" if unknown
	src     string // "file.s:NN" covering retAddr, "" if no source map
}

// fetchStack issues one `stackTrace` request and parses the chippy stack-page
// frames out of the response (issue #449). Frame 0 (the live PC) is not a
// stack-page entry, so it's filtered by the empty ChippyStackAddr.
// Transport-agnostic: remarshal handles both the wire client's JSON body and
// the inproc client's struct.
func fetchStack(c dapRequester) (StackSnapshot, error) {
	resp, err := c.Request("stackTrace", map[string]any{"threadId": 1})
	if err != nil {
		return StackSnapshot{}, err
	}
	if !resp.Success {
		return StackSnapshot{}, fmt.Errorf("stackTrace: %s", resp.Message)
	}
	var sb struct {
		StackFrames []struct {
			InstructionPointerReference string `json:"instructionPointerReference"`
			ChippyStackAddr             string `json:"chippyStackAddr"`
			ChippyCallee                string `json:"chippyCallee"`
			Line                        int    `json:"line"`
			Source                      *struct {
				Name string `json:"name"`
			} `json:"source"`
		} `json:"stackFrames"`
	}
	if err := remarshal(resp.Body, &sb); err != nil {
		return StackSnapshot{}, fmt.Errorf("stackTrace body: %w", err)
	}
	var ss StackSnapshot
	for _, f := range sb.StackFrames {
		if f.ChippyStackAddr == "" {
			continue // frame 0 = live PC, not a stack-page entry
		}
		addrLo, ok := parseDollarHex16(f.ChippyStackAddr)
		if !ok {
			continue
		}
		ret, _ := parseDollarHex16(f.InstructionPointerReference)
		fr := stackFrame{addrLo: addrLo, retAddr: ret, callee: f.ChippyCallee}
		if f.Source != nil && f.Line > 0 {
			fr.src = fmt.Sprintf("%s:%d", f.Source.Name, f.Line)
		}
		ss.Frames = append(ss.Frames, fr)
	}
	return ss, nil
}

// syncStack refreshes m.Stack from the active Source via a DAP stackTrace
// round-trip (issue #449). Mirrors syncRegs: skipped during a remote free-run
// (the server owns the CPU and the stopped event reconciles), polled every
// tick locally through the sub-microsecond inproc client. stackView renders
// the cached snapshot, so View stays pure.
func (m *Model) syncStack() {
	if m.Source == nil {
		return
	}
	if m.Running && m.Source.Attached() {
		return
	}
	ss, err := m.Source.Stack()
	if err != nil {
		return
	}
	// Pull the raw stack page so byte/run rows render DAP-sourced bytes too
	// (issue #461), not a direct m.RAM.Read.
	if page, perr := m.Source.ReadMemory(0x0100, 0x100); perr == nil {
		ss.Page = page
	}
	m.Stack = ss
}

// stackByte returns a stack-page byte ($0100-$01FF) from the DAP-sourced
// snapshot, falling back to 0 before the first sync.
func (m Model) stackByte(addr uint16) byte {
	i := int(addr & 0xFF)
	if i < len(m.Stack.Page) {
		return m.Stack.Page[i]
	}
	return 0
}

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

// stackEntries interleaves the DAP-sourced frames (m.Stack) with the runs of
// non-frame bytes between them, walking upward from SP+1 to the top of the
// stack page. Frame rows consume two bytes; consecutive non-frame bytes are
// collapsed into a single run row. Stops at the panel-row budget or $01FF.
//
// The frames come from a `stackTrace` round-trip (issue #449) — the panel no
// longer runs cpu.DetectStackFrame / symbol / source-map lookups itself; it
// only positions the snapshot frames over the stack page and renders the
// gaps as runs (raw bytes still read from the DAP-fed RAM mirror).
func (m Model) stackEntries(maxRows int) []stackEntry {
	out := make([]stackEntry, 0, maxRows)
	// Frames are only surfaced when annotation is on; otherwise the whole
	// window collapses into runs.
	var frames []stackFrame
	if m.StackAnnotate {
		frames = m.Stack.Frames
	}
	cursor := uint16(0x0100) | (uint16(m.Regs.SP) + 1)
	runStart := uint16(0)
	runLen := 0
	flushRun := func() {
		if runLen == 0 {
			return
		}
		out = append(out, stackEntry{addrLo: runStart, bytes: runLen})
		runLen = 0
	}
	fi := 0
	for cursor <= 0x01FF && len(out) < maxRows {
		// Skip any frame that starts below the live stack top (shouldn't
		// happen — the server walks from the same SP — but stay defensive
		// so a stale snapshot can't wedge the cursor).
		for fi < len(frames) && frames[fi].addrLo < cursor {
			fi++
		}
		if fi < len(frames) && frames[fi].addrLo == cursor {
			flushRun()
			if len(out) >= maxRows {
				break
			}
			f := frames[fi]
			out = append(out, stackEntry{
				isFrame: true,
				addrLo:  f.addrLo,
				bytes:   2,
				retAddr: f.retAddr,
				callee:  f.callee,
				src:     f.src,
			})
			fi++
			cursor += 2
			continue
		}
		if runLen == 0 {
			runStart = cursor
		}
		runLen++
		cursor++
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
			spByte := uint16(m.Regs.SP) + 1 + uint16(i)
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
				m.stackByte(sp))
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
				m.stackByte(e.addrLo))
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
