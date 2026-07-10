package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// promptStyle renders the `:` line at the bottom (replaces status bar when active).
var promptStyle = lipgloss.NewStyle().
	Background(lipgloss.Color("236")).
	Foreground(lipgloss.Color("231")).
	Padding(0, 1)

// updatePrompt handles keystrokes while the `:` command line is active.
// Reverse-i-search sub-mode takes precedence when active.
func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.RISearchActive {
		return m.updateRISearch(msg)
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.PromptActive = false
		m.PromptBuf = ""
		m.HistIdx = -1
		m.HistTemp = ""
		m.Status = "cancelled"
		return m, m.scheduleTick()
	case "enter":
		line := strings.TrimSpace(m.PromptBuf)
		m.appendHistory(line)
		out := m.runCommand(line)
		m.PromptActive = false
		m.PromptBuf = ""
		m.HistIdx = -1
		m.HistTemp = ""
		m.Status = out
		return m, m.scheduleTick()
	case "backspace":
		if len(m.PromptBuf) > 0 {
			m.PromptBuf = m.PromptBuf[:len(m.PromptBuf)-1]
		}
		return m, m.scheduleTick()
	case "up":
		m.historyBack()
		return m, m.scheduleTick()
	case "down":
		m.historyForward()
		return m, m.scheduleTick()
	case "tab":
		if completed, ok := completePrompt(m.PromptBuf, m.Syms); ok {
			m.PromptBuf = completed
		}
		return m, m.scheduleTick()
	case "ctrl+r":
		if len(m.History) > 0 {
			m.RISearchActive = true
			m.RISearchBuf = ""
			m.RIOrigBuf = m.PromptBuf
			m.RIMatchIdx = -1
		}
		return m, m.scheduleTick()
	}
	if len(msg.Runes) > 0 {
		m.PromptBuf += string(msg.Runes)
	}
	return m, m.scheduleTick()
}

// updateRISearch owns input while Ctrl-R reverse-incremental search is open.
// Ctrl-R again walks to the next older match; Esc restores the original
// buffer; Enter accepts the current match and runs it.
func (m Model) updateRISearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case "esc", "ctrl+c":
		m.PromptBuf = m.RIOrigBuf
		m.resetRISearch()
		return m, m.scheduleTick()
	case "enter":
		m.resetRISearch()
		line := strings.TrimSpace(m.PromptBuf)
		m.appendHistory(line)
		out := m.runCommand(line)
		m.PromptActive = false
		m.PromptBuf = ""
		m.HistIdx = -1
		m.HistTemp = ""
		m.Status = out
		return m, m.scheduleTick()
	case "backspace":
		if len(m.RISearchBuf) > 0 {
			m.RISearchBuf = m.RISearchBuf[:len(m.RISearchBuf)-1]
			m.refreshRIMatch()
		}
		return m, m.scheduleTick()
	case "ctrl+r":
		m.advanceRIMatch()
		return m, m.scheduleTick()
	}
	if len(msg.Runes) > 0 {
		m.RISearchBuf += string(msg.Runes)
		m.refreshRIMatch()
	}
	return m, m.scheduleTick()
}

func (m *Model) resetRISearch() {
	m.RISearchActive = false
	m.RISearchBuf = ""
	m.RIOrigBuf = ""
	m.RIMatchIdx = -1
}

// appendHistory records a committed prompt line. No-ops on empty input or
// when the line duplicates the most-recent entry. Persists immediately so
// a crash doesn't lose recent work.
func (m *Model) appendHistory(line string) {
	if line == "" {
		return
	}
	if n := len(m.History); n > 0 && m.History[n-1] == line {
		return
	}
	m.History = append(m.History, line)
	if len(m.History) > histCap {
		m.History = m.History[len(m.History)-histCap:]
	}
	_ = saveHistory(m.HistPath, m.History)
}

func (m *Model) historyBack() {
	if len(m.History) == 0 {
		return
	}
	switch {
	case m.HistIdx == -1:
		m.HistTemp = m.PromptBuf
		m.HistIdx = 0
	case m.HistIdx < len(m.History)-1:
		m.HistIdx++
	default:
		return
	}
	m.PromptBuf = m.History[len(m.History)-1-m.HistIdx]
}

func (m *Model) historyForward() {
	if m.HistIdx == -1 {
		return
	}
	if m.HistIdx == 0 {
		m.HistIdx = -1
		m.PromptBuf = m.HistTemp
		m.HistTemp = ""
		return
	}
	m.HistIdx--
	m.PromptBuf = m.History[len(m.History)-1-m.HistIdx]
}

// refreshRIMatch scans History newest -> oldest for a substring match of
// RISearchBuf and snaps PromptBuf to the first hit. Empty pattern clears
// the match without changing PromptBuf (so the user sees nothing yet).
func (m *Model) refreshRIMatch() {
	if m.RISearchBuf == "" {
		m.RIMatchIdx = -1
		m.PromptBuf = ""
		return
	}
	for i := len(m.History) - 1; i >= 0; i-- {
		if strings.Contains(m.History[i], m.RISearchBuf) {
			m.RIMatchIdx = i
			m.PromptBuf = m.History[i]
			return
		}
	}
	m.RIMatchIdx = -1
	m.PromptBuf = ""
}

// advanceRIMatch walks to the next older match for the current pattern.
// No-op when already at the oldest match or pattern is empty.
func (m *Model) advanceRIMatch() {
	if m.RIMatchIdx <= 0 || m.RISearchBuf == "" {
		return
	}
	for i := m.RIMatchIdx - 1; i >= 0; i-- {
		if strings.Contains(m.History[i], m.RISearchBuf) {
			m.RIMatchIdx = i
			m.PromptBuf = m.History[i]
			return
		}
	}
}

// runCommand parses a `:command args` line and applies it. Returns status text.
func (m *Model) runCommand(line string) string {
	if line == "" {
		return "ready"
	}
	parts := strings.Fields(line)
	cmd, args := parts[0], parts[1:]

	switch cmd {
	case "goto", "g":
		if len(args) == 0 {
			return "usage: :goto $XXXX"
		}
		addr, err := m.parseAddrSym(args[0])
		if err != nil {
			return err.Error()
		}
		m.MemViewAddr = addr & 0xFFF0
		m.MemCursor = addr
		return fmt.Sprintf("mem -> $%04X", addr)
	case "bank":
		if m.Banked == nil {
			return "bank select needs -cpu 65816"
		}
		if len(args) == 0 {
			return fmt.Sprintf("mem bank $%02X", m.MemViewBank)
		}
		n, err := parseByte(args[0])
		if err != nil {
			return "usage: :bank $00..$FF"
		}
		m.MemViewBank = n
		m.refreshMemWindow()
		return fmt.Sprintf("mem bank -> $%02X", m.MemViewBank)
	case "da", "disasm":
		if len(args) == 0 {
			return "usage: :da $XXXX | $BB:XXXX | symbol"
		}
		bank, off, err := m.parseDisasmTarget(args[0])
		if err != nil {
			return err.Error()
		}
		if bank != 0 && m.Banked == nil {
			return "bank addressing needs -cpu 65816"
		}
		m.DisasmFollow = false
		m.DisasmAnchor = off
		m.DisasmAnchorBank = bank
		m.syncDisasm()
		if bank != 0 {
			return fmt.Sprintf("disasm -> $%02X:%04X (' to follow PC)", bank, off)
		}
		return fmt.Sprintf("disasm -> $%04X (' to follow PC)", off)
	case "pc":
		if len(args) == 0 {
			return "usage: :pc $XXXX"
		}
		addr, err := m.parseAddrSym(args[0])
		if err != nil {
			return err.Error()
		}
		m.CPU.PC = addr
		m.CPU.Halted = false
		return fmt.Sprintf("PC -> $%04X", addr)
	case "run":
		if len(args) == 0 {
			return "usage: :run $XXXX"
		}
		addr, err := m.parseAddrSym(args[0])
		if err != nil {
			return err.Error()
		}
		// Set a one-shot breakpoint and start running.
		bp := newBP(addr)
		bp.HitLimit = -1
		m.Breakpoints[addr] = bp
		m.syncSourceBreakpoints()
		m.Running = true
		// In attach mode the server owns execution; explicitly tell
		// it to continue. Local mode steps via tickMsg → m.step() and
		// doesn't need an extra kick.
		if m.Source != nil && m.Source.Attached() {
			if err := m.Source.Continue(); err != nil {
				m.Running = false
				return fmt.Sprintf("continue: %v", err)
			}
		}
		return fmt.Sprintf("running to $%04X", addr)
	case "find":
		return m.cmdFind(args, +1)
	case "rfind":
		return m.cmdFind(args, -1)
	case "cycle":
		return m.cmdCycle(args)
	case "rewind":
		return m.cmdRewind(args)
	case "rewind-budget":
		return m.cmdRewindBudget(args)
	case "watch", "w":
		if len(args) == 0 {
			return "usage: :watch $XXXX [byte|word] [label]  |  :watch reg <name> [label]"
		}
		// Register watch: :watch reg <name> [label]
		if strings.ToLower(args[0]) == "reg" {
			if len(args) < 2 {
				return "usage: :watch reg <A|X|Y|P|SP|PC> [label]"
			}
			reg := strings.ToUpper(args[1])
			switch reg {
			case "A", "X", "Y", "P", "SP", "S", "PC":
				// ok
			default:
				return fmt.Sprintf("unknown register: %s", reg)
			}
			if reg == "S" {
				reg = "SP"
			}
			label := ""
			if len(args) >= 3 {
				label = strings.Join(args[2:], " ")
			}
			m.Watches = append(m.Watches, Watch{Kind: "reg", Reg: reg, Label: label})
			m.saveState()
			return fmt.Sprintf("watch +reg %s", reg)
		}
		addr, err := m.parseAddrSym(args[0])
		if err != nil {
			return err.Error()
		}
		// Struct overlay: :watch X [label] as {field:width, ...}  (#409).
		// Everything after `as` is the brace spec; tokens between the addr and
		// `as` are the label (defaults to the symbol name).
		for i := 1; i < len(args); i++ {
			if strings.ToLower(args[i]) != "as" {
				continue
			}
			fields, ferr := parseStructSpec(strings.Join(args[i+1:], " "))
			if ferr != nil {
				return ferr.Error()
			}
			label := strings.Join(args[1:i], " ")
			if label == "" && m.Syms != nil {
				label = m.Syms.Lookup(addr)
			}
			m.Watches = append(m.Watches, Watch{Kind: "mem", Addr: addr, Label: label, Width: 1, Fields: fields})
			m.saveState()
			return fmt.Sprintf("watch +$%04X {%d}", addr, len(fields))
		}
		width := 1
		count := 0
		var rest []string
		for _, a := range args[1:] {
			switch a {
			case "word", "w", "16":
				width = 2
			case "byte", "b", "8":
				width = 1
			default:
				if n, ok := parseCountToken(a); ok {
					count = n
					continue
				}
				rest = append(rest, a)
			}
		}
		label := strings.Join(rest, " ")
		if label == "" && m.Syms != nil {
			label = m.Syms.Lookup(addr)
		}
		// Seed an array watch from the cc65 `size=` when the user didn't
		// pass an explicit `xN`. Only fires when the symbol spans more than
		// one element; most data globals lack `size=` so this is best-effort.
		if count == 0 && m.Syms != nil {
			if sz := m.Syms.Size(addr); sz > width {
				count = sz / width
			}
		}
		if count > maxWatchCount {
			count = maxWatchCount
		}
		m.Watches = append(m.Watches, Watch{Kind: "mem", Addr: addr, Label: label, Width: width, Count: count})
		m.saveState()
		if count > 1 {
			return fmt.Sprintf("watch +$%04X [%d]", addr, count)
		}
		return fmt.Sprintf("watch +$%04X", addr)
	case "rmwatch", "unwatch":
		if len(args) == 0 {
			return "usage: :rmwatch $XXXX  |  :rmwatch reg <name>"
		}
		if strings.ToLower(args[0]) == "reg" {
			if len(args) < 2 {
				return "usage: :rmwatch reg <name>"
			}
			reg := strings.ToUpper(args[1])
			if reg == "S" {
				reg = "SP"
			}
			out := m.Watches[:0]
			for _, w := range m.Watches {
				if w.Kind != "reg" || w.Reg != reg {
					out = append(out, w)
				}
			}
			m.Watches = out
			m.saveState()
			return fmt.Sprintf("watch -reg %s", reg)
		}
		addr, err := m.parseAddrSym(args[0])
		if err != nil {
			return err.Error()
		}
		out := m.Watches[:0]
		for _, w := range m.Watches {
			if w.Kind == "reg" || w.Addr != addr {
				out = append(out, w)
			}
		}
		m.Watches = out
		m.saveState()
		return fmt.Sprintf("watch -$%04X", addr)
	case "clearwatch":
		m.Watches = nil
		m.saveState()
		return "watches cleared"
	case "speed":
		if len(args) == 0 {
			return fmt.Sprintf("speed: %s", speedLabel(m.TargetHz))
		}
		hz, err := strconv.Atoi(args[0])
		if err != nil {
			return "usage: :speed <Hz> (0=max)"
		}
		m.TargetHz = hz
		m.lastRunNS = 0
		m.cycleDebt = 0
		return fmt.Sprintf("speed: %s", speedLabel(hz))
	case "bp":
		if len(args) == 0 {
			return fmt.Sprintf("%d breakpoints", len(m.Breakpoints))
		}
		return m.cmdBP(args)
	case "syms", "symbols":
		if m.Syms == nil || !m.Syms.Has() {
			return "no symbols loaded (pass -dbg PATH or assemble with -g)"
		}
		m.ShowSyms = true
		m.SymsCursor = 0
		m.SymsOffset = 0
		if len(args) > 0 {
			m.SymsFilter = strings.Join(args, " ")
		} else {
			m.SymsFilter = ""
		}
		return "symbols"
	case "bpr":
		return m.cmdMemBP(args, MemBPRead)
	case "bpw":
		return m.cmdMemBP(args, MemBPWrite)
	case "bprw":
		return m.cmdMemBP(args, MemBPReadWrite)
	case "rmbpr", "rmbpw", "rmbprw":
		if len(args) == 0 {
			return "usage: :" + cmd + " ADDR"
		}
		addr, err := m.parseAddrSym(args[0])
		if err != nil {
			return err.Error()
		}
		if _, ok := m.MemBPs[addr]; !ok {
			return fmt.Sprintf("no mem bp at $%04X", addr)
		}
		delete(m.MemBPs, addr)
		m.saveState()
		return fmt.Sprintf("mem bp -$%04X", addr)
	case "mem":
		return m.cmdMem(args)
	case "trace":
		return m.cmdTrace(args)
	case "textsave":
		return m.cmdTextSave(args)
	case "theme":
		return m.cmdTheme(args)
	case "dap":
		return m.cmdDAP(args)
	case "help", "?":
		m.ShowHelp = true
		return "help"
	case "q", "quit":
		return "use q outside prompt to quit"
	}
	return fmt.Sprintf("unknown: %s", cmd)
}

// cmdTrace implements `:trace`. No args: report status. `on [PATH]`,
// `off`, or a bare `PATH` (shorthand for `on PATH`). Setting a path opens
// a fresh file (truncating any existing) and the tracer keeps its
// enabled/disabled state until Enable/Disable is called.
// cmdMem implements `:mem ADDR VALUE [VALUE...]` — writes hex bytes
// starting at ADDR via the CPU's bus so any MMIO peripheral / WBus
// watch side effects fire (matches the `STA $XXXX` codepath).
//
//	:mem $0200 41 42 43        → "ABC" at $0200..$0202
//	:mem main 00               → zero the byte at symbol `main`
func (m *Model) cmdMem(args []string) string {
	if len(args) < 2 {
		return "usage: :mem $ADDR VALUE [VALUE...]"
	}
	addr, err := m.parseAddrSym(args[0])
	if err != nil {
		return err.Error()
	}
	values := make([]byte, 0, len(args)-1)
	for _, raw := range args[1:] {
		v, err := parseByte(raw)
		if err != nil {
			return fmt.Sprintf("bad byte %q: %v", raw, err)
		}
		values = append(values, v)
	}
	for i, v := range values {
		dst := addr + uint16(i)
		m.memWrite(dst, v)
	}
	m.saveState()
	if len(values) == 1 {
		return fmt.Sprintf("$%04X <- $%02X", addr, values[0])
	}
	return fmt.Sprintf("$%04X..$%04X <- %d bytes", addr, addr+uint16(len(values)-1), len(values))
}

// parseByte accepts $XX / 0xXX / decimal 0-255. Returns an error on
// out-of-range so users can't accidentally write a 16-bit value.
// maxWatchCount caps array-watch expansion so a bogus `size=` or a fat-
// fingered `xN` can't flood the watch panel.
const maxWatchCount = 256

// parseCountToken recognises the array-length token on `:watch`, written
// as `xN` (e.g. `x4`) or `[N]` (e.g. `[4]`). Returns (n, true) on a match
// with n >= 1; (0, false) otherwise so the caller treats it as a label word.
func parseCountToken(s string) (int, bool) {
	switch {
	case strings.HasPrefix(s, "x"), strings.HasPrefix(s, "X"):
		s = s[1:]
	case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
		s = s[1 : len(s)-1]
	default:
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// maxStructFields caps members in a struct-overlay watch (#409) so a bogus
// spec can't flood the watch panel.
const maxStructFields = 32

// parseStructSpec parses the `{field:width, ...}` body of a struct-overlay
// watch. Each member is `name[@offset]:width` where width is byte|word (or
// b/w/8/16). Offsets auto-advance by width unless an explicit `@N` overrides
// the running cursor (decimal or $hex).
func parseStructSpec(spec string) ([]WatchField, error) {
	spec = strings.TrimSpace(spec)
	if !strings.HasPrefix(spec, "{") || !strings.HasSuffix(spec, "}") {
		return nil, fmt.Errorf("struct overlay must be {field:width, ...}")
	}
	body := strings.TrimSpace(spec[1 : len(spec)-1])
	var fields []WatchField
	off := 0
	for _, raw := range strings.Split(body, ",") {
		member := strings.TrimSpace(raw)
		if member == "" {
			continue
		}
		f, err := parseStructField(member, off)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
		off = f.Offset + f.Width
		if len(fields) > maxStructFields {
			return nil, fmt.Errorf("too many fields (max %d)", maxStructFields)
		}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty struct overlay")
	}
	return fields, nil
}

func parseStructField(s string, defaultOffset int) (WatchField, error) {
	colon := strings.IndexByte(s, ':')
	if colon < 0 {
		return WatchField{}, fmt.Errorf("field %q needs name:width", s)
	}
	namePart := strings.TrimSpace(s[:colon])
	typePart := strings.TrimSpace(s[colon+1:])
	offset := defaultOffset
	if at := strings.IndexByte(namePart, '@'); at >= 0 {
		o, err := parseOffset(namePart[at+1:])
		if err != nil {
			return WatchField{}, fmt.Errorf("field %q: bad offset: %v", s, err)
		}
		offset = o
		namePart = strings.TrimSpace(namePart[:at])
	}
	if namePart == "" {
		return WatchField{}, fmt.Errorf("field %q missing name", s)
	}
	width, ok := parseWidthToken(typePart)
	if !ok {
		return WatchField{}, fmt.Errorf("field %q: width must be byte|word", s)
	}
	return WatchField{Name: namePart, Offset: offset, Width: width}, nil
}

// parseWidthToken maps a member width keyword to a byte count.
func parseWidthToken(s string) (int, bool) {
	switch strings.ToLower(s) {
	case "byte", "b", "8", "u8":
		return 1, true
	case "word", "w", "16", "u16":
		return 2, true
	}
	return 0, false
}

// parseOffset accepts a decimal or $hex / 0xhex byte offset.
func parseOffset(s string) (int, error) {
	s = strings.TrimSpace(s)
	var v uint64
	var err error
	switch {
	case strings.HasPrefix(s, "$"):
		v, err = strconv.ParseUint(s[1:], 16, 16)
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		v, err = strconv.ParseUint(s[2:], 16, 16)
	default:
		v, err = strconv.ParseUint(s, 10, 16)
	}
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

func parseByte(s string) (byte, error) {
	var v uint64
	var err error
	switch {
	case strings.HasPrefix(s, "$"):
		v, err = strconv.ParseUint(s[1:], 16, 16)
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		v, err = strconv.ParseUint(s[2:], 16, 16)
	default:
		v, err = strconv.ParseUint(s, 10, 16)
	}
	if err != nil {
		return 0, err
	}
	if v > 0xFF {
		return 0, fmt.Errorf("value $%X exceeds byte range", v)
	}
	return byte(v), nil
}

// memWrite is the canonical "TUI wrote one byte" path. Prefer the
// WBus wrapper (so memory watches fire) over the raw RAM if WBus is
// wired; otherwise fall through to the CPU's bus (which may be MMIO
// in chippy-as-library use cases) so peripherals see the write.
// Direct RAM.Write is the last fallback for the legacy in-process
// case where no bus is configured.
// parseDisasmTarget resolves a `:da` argument to a (bank, offset) pair.
// Accepts `$BB:XXXX` (bank + 16-bit offset), a bare 24-bit hex like `$029000`
// (split into bank + offset), or a symbol / 16-bit address (bank 0).
func (m *Model) parseDisasmTarget(s string) (byte, uint16, error) {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ':'); i > 0 {
		b, err := parseByte(s[:i])
		if err != nil {
			return 0, 0, fmt.Errorf("bad bank %q", s[:i])
		}
		off, err := m.parseAddrSym(s[i+1:])
		if err != nil {
			return 0, 0, err
		}
		return b, off, nil
	}
	// A bare 24-bit hex ($029000) splits into bank + offset; smaller values and
	// symbols fall through to the 16-bit / symbol resolver (bank 0).
	hex := strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(s), "$"), "0x")
	if v, err := strconv.ParseUint(hex, 16, 32); err == nil && v > 0xFFFF && v <= 0xFFFFFF {
		return byte(v >> 16), uint16(v), nil
	}
	off, err := m.parseAddrSym(s)
	if err != nil {
		return 0, 0, err
	}
	return 0, off, nil
}

func (m *Model) memWrite(addr uint16, v byte) {
	// Banks 1-255 (65816) write through the bank-aware bus; bank 0 keeps the
	// 16-bit chain so watchpoints still see the poke (#505).
	if m.MemViewBank != 0 && m.Banked != nil {
		m.Banked.Write24(uint32(m.MemViewBank)<<16|uint32(addr), v)
		return
	}
	switch {
	case m.WBus != nil:
		m.WBus.Write(addr, v)
	case m.CPU != nil && m.CPU.Bus != nil:
		m.CPU.Bus.Write(addr, v)
	default:
		m.RAM.Write(addr, v)
	}
}

func (m *Model) cmdTrace(args []string) string {
	if m.Tracer == nil {
		return "trace unavailable"
	}
	if len(args) == 0 {
		if m.Tracer.Enabled() {
			return fmt.Sprintf("trace: on -> %s", m.Tracer.Path())
		}
		if p := m.Tracer.Path(); p != "" {
			return fmt.Sprintf("trace: off (last: %s)", p)
		}
		return "trace: off (no path)"
	}
	switch strings.ToLower(args[0]) {
	case "on":
		if len(args) >= 2 {
			if err := m.Tracer.SetPath(args[1]); err != nil {
				return fmt.Sprintf("trace: %v", err)
			}
		}
		if m.Tracer.Path() == "" {
			return "trace: no path — use :trace on PATH"
		}
		m.Tracer.Enable()
		return fmt.Sprintf("trace: on -> %s", m.Tracer.Path())
	case "off":
		m.Tracer.Disable()
		return "trace: off"
	default:
		if err := m.Tracer.SetPath(args[0]); err != nil {
			return fmt.Sprintf("trace: %v", err)
		}
		m.Tracer.Enable()
		return fmt.Sprintf("trace: on -> %s", m.Tracer.Path())
	}
}

// parseAddrSym tries numeric forms then symbol lookup via m.Syms.
func (m *Model) parseAddrSym(s string) (uint16, error) {
	if a, err := parseAddr(s); err == nil {
		return a, nil
	}
	if m.Syms != nil {
		if a, ok := m.Syms.LookupName(s); ok {
			return a, nil
		}
	}
	return 0, fmt.Errorf("bad address or symbol: %s", s)
}

// parseAddr accepts $XXXX, 0xXXXX, plain hex (XXXX), decimal, or a symbol name.
func parseAddr(s string) (uint16, error) {
	if s == "" {
		return 0, fmt.Errorf("empty address")
	}
	orig := s
	// Try symbol lookup later; first numeric forms.
	switch {
	case strings.HasPrefix(s, "$"):
		s = s[1:]
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		s = s[2:]
	}
	if v, err := strconv.ParseUint(s, 16, 32); err == nil {
		if v > 0xFFFF {
			return 0, fmt.Errorf("address $%X out of range", v)
		}
		return uint16(v), nil
	}
	if v, err := strconv.ParseUint(s, 10, 32); err == nil {
		if v > 0xFFFF {
			return 0, fmt.Errorf("address %d out of range", v)
		}
		return uint16(v), nil
	}
	return 0, fmt.Errorf("bad address: %s", orig)
}

// promptLine renders the `:` line that replaces the status bar when active.
// Reverse-i-search shows `(reverse-i-search)\`pattern': match` instead.
func (m Model) promptLine(width int) string {
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	if m.RISearchActive {
		text := fmt.Sprintf("(reverse-i-search)`%s': %s%s", m.RISearchBuf, m.PromptBuf, cursor)
		return promptStyle.Width(width).Render(text)
	}
	text := ":" + m.PromptBuf + cursor
	return promptStyle.Width(width).Render(text)
}

// cmdTheme switches the active color palette. No arg reports the
// current theme; `default | mono | protan | tritan` sets it; saved
// state persists the choice across launches.
//
//	:theme
//	:theme mono
func (m *Model) cmdTheme(args []string) string {
	if len(args) == 0 {
		current := m.Theme
		if current == "" {
			current = string(ThemeDefault)
		}
		return fmt.Sprintf("theme: %s (available: %s)", current, strings.Join(AvailableThemes(), ", "))
	}
	resolved := resolveTheme(args[0])
	applyTheme(resolved)
	m.Theme = string(resolved)
	m.saveState()
	return fmt.Sprintf("theme -> %s", m.Theme)
}

// cmdTextSave dumps the TextOutput buffer to a file. Use case: a
// long-running demo whose output keeps wrapping past the buffer cap.
//
//	:textsave PATH
func (m *Model) cmdTextSave(args []string) string {
	if m.TextOut == nil {
		return "textsave: no TextOutput peripheral"
	}
	if len(args) == 0 {
		return "usage: :textsave PATH"
	}
	path := args[0]
	if err := os.WriteFile(path, m.TextOut.Bytes(), 0o644); err != nil {
		return fmt.Sprintf("textsave: %v", err)
	}
	return fmt.Sprintf("textsave -> %s (%d bytes)", path, m.TextOut.Len())
}
