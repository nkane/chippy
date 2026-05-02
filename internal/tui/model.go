package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/symbols"
)

// styles
var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("213"))
	panelStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	regStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	flagOn     = lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true)
	flagOff    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	curLine    = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("226")).Bold(true)
	bpLine     = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	help       = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	statusBar  = lipgloss.NewStyle().Background(lipgloss.Color("57")).Foreground(lipgloss.Color("231")).Padding(0, 1)
	labelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("207")).Bold(true)
	dimAddr    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	memBPRead  = lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)  // 👁 blue
	memBPWrite = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true) // ✏ red
	memBPRW    = lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true) // 🔁 magenta
)

const (
	runTick  = 16 * time.Millisecond
	idleTick = 100 * time.Millisecond
)

type tickMsg struct{}

// Watch is a pinned memory address shown in the watch panel.
type Watch struct {
	// Kind is "mem" (default) or "reg". Empty string treated as "mem" for
	// backward compat with state files written before registers existed.
	Kind  string `json:"kind,omitempty"`
	Addr  uint16 `json:"addr,omitempty"`
	Label string `json:"label,omitempty"`
	Width int    `json:"width,omitempty"` // mem: 1 = byte, 2 = word (LE)
	Reg   string `json:"reg,omitempty"`   // reg: A,X,Y,P,SP,PC
}

type Model struct {
	CPU         *cpu.CPU
	RAM         *cpu.RAM
	WBus        *WBus // optional: bus wrapper that records mem watch hits
	Syms        *symbols.Table
	Running     bool
	Breakpoints map[uint16]*Breakpoint
	MemBPs      map[uint16]*MemBP
	MemViewAddr uint16
	Status      string

	// Modals
	ShowHelp bool
	ShowBPs  bool
	BPCursor int // selected row in BP manager

	// Source view
	ShowSource  bool                       // toggle source panel vs disassembly
	SourceFiles map[string][]string        // filename -> lines (1-indexed via [i-1])
	PCToSrc     map[uint16]symbols.SrcLoc  // PC -> (file, line)
	DataRanges  []symbols.Range            // [start,end) regions to render as .byte

	// Watch list
	Watches []Watch

	// Run-speed control: target Hz (0 = unthrottled).
	TargetHz  int
	lastRunNS int64
	cycleDebt int64

	// Command prompt ( `:` line editor )
	PromptActive bool
	PromptBuf    string

	// State persistence
	StatePath string

	// Disassembly viewport: when DisasmFollow is true (default), the panel
	// re-anchors on PC each frame. User scroll keys flip it off and pin
	// DisasmAnchor as the address shown at the top of the window.
	DisasmFollow bool
	DisasmAnchor uint16

	W, H int
}

type disasmCacheEntry struct {
	anchor       uint16
	above, below int
	addrs        []uint16
}

type srcLoc = symbols.SrcLoc

func New(c *cpu.CPU, r *cpu.RAM) Model {
	return Model{
		CPU:         c,
		RAM:         r,
		Breakpoints:  map[uint16]*Breakpoint{},
		MemBPs:       map[uint16]*MemBP{},
		MemViewAddr:  0x0000,
		Status:       "ready",
		TargetHz:     0,
		DisasmFollow: true,
		W:            120,
		H:            40,
	}
}

// WithWBus attaches a bus wrapper that records memory watchpoint hits. The
// CPU should already be using this same WBus as its bus. Call after New.
func (m Model) WithWBus(w *WBus) Model {
	m.WBus = w
	if w != nil {
		w.AttachCPU(m.CPU)
		w.SetWatches(m.MemBPs)
	}
	return m
}

func (m Model) WithSymbols(s *symbols.Table) Model {
	m.Syms = s
	return m
}

// WithStatePath enables persistence of breakpoints/mem cursor/watches.
func (m Model) WithStatePath(p string) Model {
	m.StatePath = p
	if p != "" {
		loadState(&m, p)
	}
	return m
}

// WithSourceMap loads PC->(file,line) mapping from cc65 .dbg file lines.
func (m Model) WithSourceMap(sm *symbols.SourceMap) Model {
	if sm == nil {
		return m
	}
	m.PCToSrc = sm.PCToSrc
	m.SourceFiles = sm.Files
	m.DataRanges = sm.DataRanges
	return m
}

func (m Model) Init() tea.Cmd { return m.scheduleTick() }

func (m Model) scheduleTick() tea.Cmd {
	d := idleTick
	if m.Running {
		d = runTick
	}
	return tea.Tick(d, func(_ time.Time) tea.Msg { return tickMsg{} })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.W, m.H = msg.Width, msg.Height
		return m, m.scheduleTick()

	case tea.KeyMsg:
		// Prompt mode owns all input until Enter/Esc.
		if m.PromptActive {
			return m.updatePrompt(msg)
		}
		// Help modal: any key dismisses.
		if m.ShowHelp {
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			}
			m.ShowHelp = false
			return m, m.scheduleTick()
		}
		// BP manager modal.
		if m.ShowBPs {
			return m.updateBPManager(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.saveState()
			return m, tea.Quit
		case "?", "h":
			m.ShowHelp = true
			m.Status = "help"
		case ":":
			m.PromptActive = true
			m.PromptBuf = ""
			m.Status = "command"
		case "B":
			m.ShowBPs = true
			m.BPCursor = 0
			m.Status = "breakpoints"
		case "v":
			m.ShowSource = !m.ShowSource
			if m.ShowSource {
				m.Status = "source view"
			} else {
				m.Status = "disasm view"
			}
		case "s":
			if m.CPU.Halted {
				m.Status = "halted (press R to reset)"
			} else {
				m.CPU.Step()
				m.statusAfterStep("stepped")
			}
		case "S":
			if m.CPU.Halted {
				m.Status = "halted (press R to reset)"
				break
			}
			for i := 0; i < 16 && !m.CPU.Halted; i++ {
				m.CPU.Step()
				if mpause, mmsg := m.processMemHits(); mpause {
					m.Status = mmsg
					break
				} else if mmsg != "" {
					m.Status = mmsg
				}
				if pause, msg := m.shouldBreakAt(m.CPU.PC); pause {
					break
				} else if msg != "" {
					m.Status = msg
				}
			}
			m.statusAfterStep("stepped x16")
		case "n":
			m.stepOver()
		case "f":
			m.runToNextLine()
		case "r":
			if m.CPU.Halted {
				m.Status = "halted (press R to reset)"
				break
			}
			m.Running = !m.Running
			if m.Running {
				m.Status = fmt.Sprintf("running @ %s", speedLabel(m.TargetHz))
			} else {
				m.Status = "paused"
			}
		case "R":
			m.CPU.Reset()
			m.Status = "reset"
		case "b":
			if _, ok := m.Breakpoints[m.CPU.PC]; ok {
				delete(m.Breakpoints, m.CPU.PC)
			} else {
				m.Breakpoints[m.CPU.PC] = newBP(m.CPU.PC)
			}
			m.Status = fmt.Sprintf("toggle bp $%04X", m.CPU.PC)
			m.saveState()
		case "+", "=":
			m.bumpSpeed(+1)
		case "-", "_":
			m.bumpSpeed(-1)
		case "0":
			m.TargetHz = 0
			m.lastRunNS = 0
			m.cycleDebt = 0
			m.Status = "speed: max"
		case "j", "down":
			m.MemViewAddr += 0x10
		case "k", "up":
			m.MemViewAddr -= 0x10
		case "J", "pgdown":
			m.MemViewAddr += 0x100
		case "K", "pgup":
			m.MemViewAddr -= 0x100
		case "g":
			m.MemViewAddr = 0
		case "G":
			m.MemViewAddr = 0xFF00
		case "[":
			m.disasmScroll(-1)
		case "]":
			m.disasmScroll(+1)
		case "{":
			m.disasmScroll(-8)
		case "}":
			m.disasmScroll(+8)
		case "'":
			m.DisasmFollow = true
			m.Status = "disasm: follow PC"
		}
		return m, m.scheduleTick()

	case tickMsg:
		if m.Running {
			budget := m.runBudget()
			for i := 0; i < budget; i++ {
				m.CPU.Step()
				if m.CPU.Halted {
					m.Running = false
					m.Status = fmt.Sprintf("halted at $%04X", m.CPU.PC)
					break
				}
				if mpause, mmsg := m.processMemHits(); mpause {
					m.Running = false
					m.Status = mmsg
					break
				} else if mmsg != "" {
					m.Status = mmsg
				}
				if pause, msg := m.shouldBreakAt(m.CPU.PC); pause {
					m.Running = false
					m.Status = fmt.Sprintf("hit bp $%04X", m.CPU.PC)
					break
				} else if msg != "" {
					m.Status = msg
				}
			}
		}
		return m, m.scheduleTick()
	}
	return m, nil
}

// statusAfterStep updates Status reflecting halt or normal stepping outcome.
func (m *Model) statusAfterStep(normal string) {
	if m.CPU.Halted {
		m.Status = fmt.Sprintf("halted at $%04X", m.CPU.PC)
		return
	}
	if mpause, mmsg := m.processMemHits(); mpause {
		m.Status = mmsg
		return
	} else if mmsg != "" {
		m.Status = mmsg
		return
	}
	if pause, _ := m.shouldBreakAt(m.CPU.PC); pause {
		m.Status = fmt.Sprintf("hit bp $%04X", m.CPU.PC)
		return
	}
	m.Status = normal
}

// stepOver: if current opcode is JSR, run until we return.
func (m *Model) stepOver() {
	if m.CPU.Halted {
		m.Status = "halted (press R to reset)"
		return
	}
	op := m.RAM.Read(m.CPU.PC)
	if op != 0x20 {
		m.CPU.Step()
		m.statusAfterStep("stepped")
		return
	}
	retPC := m.CPU.PC + 3
	const guard = 2_000_000
	for i := 0; i < guard; i++ {
		m.CPU.Step()
		if m.CPU.Halted {
			m.Status = fmt.Sprintf("halted at $%04X", m.CPU.PC)
			return
		}
		if m.CPU.PC == retPC {
			m.Status = fmt.Sprintf("step-over -> $%04X", retPC)
			return
		}
		if mpause, mmsg := m.processMemHits(); mpause {
			m.Status = mmsg
			return
		} else if mmsg != "" {
			m.Status = mmsg
		}
		if pause, msg := m.shouldBreakAt(m.CPU.PC); pause {
			m.Status = fmt.Sprintf("hit bp $%04X (in subroutine)", m.CPU.PC)
			return
		} else if msg != "" {
			m.Status = msg
		}
	}
	m.Status = "step-over guard hit (2M cycles)"
}

// runToNextLine: step until the source line changes (or fall back to next instr).
func (m *Model) runToNextLine() {
	if m.CPU.Halted {
		m.Status = "halted (press R to reset)"
		return
	}
	startLoc, hasMap := m.PCToSrc[m.CPU.PC]
	if !hasMap || m.PCToSrc == nil {
		m.CPU.Step()
		m.statusAfterStep("stepped (no src map)")
		return
	}
	const guard = 1_000_000
	for i := 0; i < guard; i++ {
		m.CPU.Step()
		if m.CPU.Halted {
			m.Status = fmt.Sprintf("halted at $%04X", m.CPU.PC)
			return
		}
		if mpause, mmsg := m.processMemHits(); mpause {
			m.Status = mmsg
			return
		} else if mmsg != "" {
			m.Status = mmsg
		}
		if pause, msg := m.shouldBreakAt(m.CPU.PC); pause {
			m.Status = fmt.Sprintf("hit bp $%04X", m.CPU.PC)
			return
		} else if msg != "" {
			m.Status = msg
		}
		loc, ok := m.PCToSrc[m.CPU.PC]
		if ok && (loc.File != startLoc.File || loc.Line != startLoc.Line) {
			m.Status = fmt.Sprintf("line -> %s:%d", loc.File, loc.Line)
			return
		}
	}
	m.Status = "run-to-line guard hit"
}

// runBudget returns how many CPU steps to execute this tick.
func (m *Model) runBudget() int {
	if m.TargetHz <= 0 {
		return 5000
	}
	now := time.Now().UnixNano()
	if m.lastRunNS == 0 {
		m.lastRunNS = now
		return m.TargetHz / 60
	}
	elapsed := now - m.lastRunNS
	m.lastRunNS = now
	want := int64(m.TargetHz) * elapsed / int64(time.Second)
	want += m.cycleDebt
	if want < 1 {
		m.cycleDebt = want
		return 0
	}
	cap := int64(m.TargetHz / 10)
	if cap < 1 {
		cap = 1
	}
	if want > cap {
		m.cycleDebt = want - cap
		want = cap
	} else {
		m.cycleDebt = 0
	}
	return int(want)
}

func (m *Model) bumpSpeed(dir int) {
	steps := []int{0, 1_000, 10_000, 100_000, 1_000_000, 2_000_000, 10_000_000}
	idx := 0
	for i, s := range steps {
		if s == m.TargetHz {
			idx = i
			break
		}
	}
	idx += dir
	if idx < 0 {
		idx = 0
	}
	if idx >= len(steps) {
		idx = len(steps) - 1
	}
	m.TargetHz = steps[idx]
	m.lastRunNS = 0
	m.cycleDebt = 0
	m.Status = fmt.Sprintf("speed: %s", speedLabel(m.TargetHz))
}

func speedLabel(hz int) string {
	switch {
	case hz <= 0:
		return "max"
	case hz >= 1_000_000:
		return fmt.Sprintf("%d MHz", hz/1_000_000)
	case hz >= 1_000:
		return fmt.Sprintf("%d kHz", hz/1_000)
	default:
		return fmt.Sprintf("%d Hz", hz)
	}
}

// updateBPManager handles input while the breakpoint list modal is open.
func (m Model) updateBPManager(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	bps := m.sortedBPs()
	switch msg.String() {
	case "esc", "B", "q":
		m.ShowBPs = false
		m.Status = "ready"
	case "j", "down":
		if m.BPCursor < len(bps)-1 {
			m.BPCursor++
		}
	case "k", "up":
		if m.BPCursor > 0 {
			m.BPCursor--
		}
	case "d", "x", "delete":
		if m.BPCursor < len(bps) {
			delete(m.Breakpoints, bps[m.BPCursor])
			if m.BPCursor >= len(m.Breakpoints) && m.BPCursor > 0 {
				m.BPCursor--
			}
			m.saveState()
		}
	case "e":
		if m.BPCursor < len(bps) {
			if bp := m.Breakpoints[bps[m.BPCursor]]; bp != nil {
				bp.Enabled = !bp.Enabled
				m.saveState()
			}
		}
	case "enter":
		if m.BPCursor < len(bps) {
			m.CPU.PC = bps[m.BPCursor]
			m.ShowBPs = false
			m.Status = fmt.Sprintf("PC -> $%04X", m.CPU.PC)
		}
	case "ctrl+c":
		return m, tea.Quit
	}
	return m, m.scheduleTick()
}

func (m Model) sortedBPs() []uint16 {
	out := make([]uint16, 0, len(m.Breakpoints))
	for a := range m.Breakpoints {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (m Model) sortedMemBPs() []uint16 {
	out := make([]uint16, 0, len(m.MemBPs))
	for a := range m.MemBPs {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---------- layout ----------

func (m Model) View() string {
	if m.W == 0 || m.H == 0 {
		return "initializing…"
	}

	const vMargin = 1
	const hMargin = 4
	innerH := m.H - 2 - 2*vMargin
	if innerH < 10 {
		innerH = 10
	}
	usableW := m.W - 2*hMargin
	if usableW < 60 {
		usableW = m.W
	}

	leftW := 36
	if usableW < 90 {
		leftW = usableW/3 - 2
		if leftW < 28 {
			leftW = 28
		}
	}
	gap := 2
	rightW := usableW - leftW - gap
	if rightW < 40 {
		rightW = 40
	}

	regH := 5
	flagH := 4
	watchH := 0
	if len(m.Watches) > 0 {
		watchH = len(m.Watches) + 3
		if watchH > 10 {
			watchH = 10
		}
	}
	stackH := innerH - regH - flagH - watchH
	if stackH < 6 {
		stackH = 6
	}

	disH := innerH * 6 / 10
	if disH < 10 {
		disH = 10
	}
	memH := innerH - disH
	if memH < 8 {
		memH = 8
		disH = innerH - memH
	}

	leftPanels := []string{
		m.regsView(leftW, regH),
		m.flagsView(leftW, flagH),
	}
	if watchH > 0 {
		leftPanels = append(leftPanels, m.watchView(leftW, watchH))
	}
	leftPanels = append(leftPanels, m.stackView(leftW, stackH))
	left := lipgloss.JoinVertical(lipgloss.Left, leftPanels...)

	var topRight string
	if m.ShowSource && m.PCToSrc != nil {
		topRight = m.sourceView(rightW, disH)
	} else {
		topRight = m.disasmView(rightW, disH)
	}
	right := lipgloss.JoinVertical(lipgloss.Left,
		topRight,
		m.memView(rightW, memH),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)

	title := titleStyle.Render(" chippy — 6502 TUI debugger ")
	titleRow := lipgloss.PlaceHorizontal(m.W, lipgloss.Center, title)

	statusText := fmt.Sprintf(
		" %s │ cyc=%d │ PC=$%04X │ %s │ [?] help  [:] cmd  [s/n] step  [r] run  [v] src  [q] quit",
		m.Status, m.CPU.Cycles, m.CPU.PC, speedLabel(m.TargetHz),
	)
	bar := statusBar.Width(m.W).Render(statusText)
	if m.PromptActive {
		bar = m.promptLine(m.W)
	}

	bodyHeight := innerH
	var bodyBlock string
	switch {
	case m.ShowHelp:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.helpModal())
	case m.ShowBPs:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.bpModal())
	default:
		bodyBlock = lipgloss.PlaceHorizontal(m.W, lipgloss.Center, body)
	}

	pad := strings.Repeat(" ", m.W)
	vBuf := strings.Repeat(pad+"\n", vMargin)

	return lipgloss.JoinVertical(lipgloss.Left,
		titleRow,
		strings.TrimRight(vBuf, "\n"),
		bodyBlock,
		strings.TrimRight(vBuf, "\n"),
		bar,
	)
}

const panelWChrome = 4
const panelHChrome = 2

func fitPanel(title, body string, w, h int) string {
	innerW := w - panelWChrome
	if innerW < 1 {
		innerW = 1
	}
	innerH := h - panelHChrome
	if innerH < 1 {
		innerH = 1
	}
	header := titleStyle.Render(title)
	content := header + "\n" + body
	return panelStyle.Width(innerW).Height(innerH).Render(content)
}

// ---------- modals ----------

func (m Model) helpModal() string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	sectStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("207")).Bold(true).Underline(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)

	row := func(k, d string) string {
		return fmt.Sprintf("  %s   %s", keyStyle.Render(fmt.Sprintf("%-10s", k)), descStyle.Render(d))
	}

	sections := []struct {
		title string
		rows  [][2]string
	}{
		{"Execution", [][2]string{
			{"s", "step one instruction"},
			{"S", "step 16 instructions"},
			{"n", "step over (run JSR to RTS)"},
			{"f", "run to next source line"},
			{"r", "run / pause"},
			{"R", "reset CPU"},
			{"b", "toggle breakpoint at PC"},
			{"B", "breakpoint manager"},
		}},
		{"Speed", [][2]string{
			{"+", "faster"},
			{"-", "slower"},
			{"0", "unthrottled (max)"},
		}},
		{"Memory view", [][2]string{
			{"j / ↓", "scroll down by $10"},
			{"k / ↑", "scroll up by $10"},
			{"J / PgDn", "scroll down by $100"},
			{"K / PgUp", "scroll up by $100"},
			{"g / G", "jump to $0000 / $FF00"},
		}},
		{"Disassembly", [][2]string{
			{"[ / ]", "scroll one instruction up / down"},
			{"{ / }", "scroll eight instructions up / down"},
			{"'", "follow PC again"},
		}},
		{"General", [][2]string{
			{":", "command line (:goto :pc :watch :speed :bp)"},
			{"v", "toggle source / disassembly view"},
			{"? / h", "toggle this help"},
			{"q / ^C", "quit"},
		}},
		{"Breakpoints", [][2]string{
			{":bp X", "toggle plain bp at addr/symbol/file.s:42"},
			{":bp X once", "one-shot (auto-delete on hit)"},
			{":bp X hits N", "break only on Nth hit"},
			{":bp X if E", "conditional (E uses A,X,Y,P,SP,PC,N,V,Z,C,[$XX])"},
			{":bp X log M", "log point: prints M, doesn't pause"},
			{"sigils", "🛑 plain  🔶 cond  📜 log  💩 reject  👉 PC"},
		}},
		{"Memory watchpoints", [][2]string{
			{":bpr X", "watch reads at X"},
			{":bpw X", "watch writes at X"},
			{":bprw X", "watch both reads and writes"},
			{":rmbpr X", "remove (also :rmbpw / :rmbprw)"},
			{"modifiers", "same: once / hits N / if E / log M"},
			{"sigils", "👁 read  ✏ write  🔁 read+write"},
		}},
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("chippy — keybindings"))
	b.WriteString("\n\n")
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(sectStyle.Render(s.title))
		b.WriteString("\n")
		for _, kv := range s.rows {
			b.WriteString(row(kv[0], kv[1]))
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(dim.Render("  press any key to dismiss"))

	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("213")).
		Padding(1, 3).
		Render(b.String())
}

func (m Model) bpModal() string {
	bps := m.sortedBPs()
	var b strings.Builder
	b.WriteString(titleStyle.Render("Breakpoints"))
	b.WriteString("\n\n")
	if len(bps) == 0 {
		b.WriteString(help.Render("  (none — press `b` at PC to add)"))
	} else {
		for i, addr := range bps {
			bp := m.Breakpoints[addr]
			marker := bp.marker()
			var line string
			if bp.Rejected {
				line = fmt.Sprintf("%s (unresolved)", marker)
			} else {
				line = fmt.Sprintf("%s $%04X", marker, addr)
				if m.Syms != nil {
					if name := m.Syms.Lookup(addr); name != "" {
						line += "  " + labelStyle.Render(name)
					}
				}
			}
			if bp.Source != "" {
				line += dimAddr.Render("  " + bp.Source)
			}
			if !bp.Enabled {
				line += dimAddr.Render("  [disabled]")
			}
			if bp.HitLimit > 0 {
				line += dimAddr.Render(fmt.Sprintf("  [%d/%d]", bp.Hits, bp.HitLimit))
			} else if bp.HitLimit == -1 {
				line += dimAddr.Render("  [once]")
			} else if bp.Hits > 0 {
				line += dimAddr.Render(fmt.Sprintf("  [%d hits]", bp.Hits))
			}
			if bp.Cond != "" {
				line += dimAddr.Render("  if " + bp.Cond)
			}
			if bp.Log != "" {
				line += dimAddr.Render("  log " + bp.Log)
			}
			if i == m.BPCursor {
				line = curLine.Render(line)
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n")
	if len(m.MemBPs) > 0 {
		b.WriteString(titleStyle.Render("Memory watchpoints"))
		b.WriteString("\n\n")
		mbps := m.sortedMemBPs()
		for _, addr := range mbps {
			line := m.MemBPs[addr].describe()
			if m.Syms != nil {
				if name := m.Syms.Lookup(addr); name != "" {
					line += "  " + labelStyle.Render(name)
				}
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(help.Render("  j/k move  d delete  e enable/disable  enter set PC  esc close"))
	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("196")).
		Padding(1, 3).
		Render(b.String())
}

// ---------- panels ----------

func (m Model) regsView(w, h int) string {
	c := m.CPU
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	row := func(label string, hex string) string {
		return regStyle.Render(label) + " " + val.Render(hex)
	}
	state := lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true).Render("RUN ")
	if c.Halted {
		state = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("HALT")
	} else if m.Running {
		state = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render("RUN*")
	}
	body := fmt.Sprintf("%s   %s   %s\n%s   %s   %s",
		row("A: ", fmt.Sprintf("$%02X", c.A)),
		row("X: ", fmt.Sprintf("$%02X", c.X)),
		row("Y: ", fmt.Sprintf("$%02X", c.Y)),
		row("SP:", fmt.Sprintf("$%02X", c.SP)),
		row("PC:", fmt.Sprintf("$%04X", c.PC)),
		state,
	)
	return fitPanel("Registers", body, w, h)
}

func (m Model) flagsView(w, h int) string {
	c := m.CPU
	flag := func(name string, on bool) string {
		if on {
			return flagOn.Render(name)
		}
		return flagOff.Render(strings.ToLower(name))
	}
	body := fmt.Sprintf("%s %s %s %s  %s %s %s %s",
		flag("N", c.P&cpu.FlagN != 0),
		flag("V", c.P&cpu.FlagV != 0),
		flag("U", c.P&cpu.FlagU != 0),
		flag("B", c.P&cpu.FlagB != 0),
		flag("D", c.P&cpu.FlagD != 0),
		flag("I", c.P&cpu.FlagI != 0),
		flag("Z", c.P&cpu.FlagZ != 0),
		flag("C", c.P&cpu.FlagC != 0),
	)
	return fitPanel("Flags", body, w, h)
}

func (m Model) watchView(w, h int) string {
	var b strings.Builder
	for _, wt := range m.Watches {
		var val, name string
		if wt.Kind == "reg" {
			val = m.regValue(wt.Reg)
			name = wt.Label
			if name == "" {
				name = wt.Reg
			}
		} else {
			if wt.Width == 2 {
				lo := uint16(m.RAM.Read(wt.Addr))
				hi := uint16(m.RAM.Read(wt.Addr + 1))
				val = fmt.Sprintf("$%04X", lo|(hi<<8))
			} else {
				val = fmt.Sprintf("  $%02X", m.RAM.Read(wt.Addr))
			}
			name = wt.Label
			if name == "" {
				name = fmt.Sprintf("$%04X", wt.Addr)
			}
		}
		b.WriteString(fmt.Sprintf("%s %s\n",
			dimAddr.Render(fmt.Sprintf("%-10s", name)),
			lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true).Render(val)))
	}
	return fitPanel("Watch", strings.TrimRight(b.String(), "\n"), w, h)
}

// regValue formats the named CPU register. Returns "?" for unknown names.
func (m Model) regValue(name string) string {
	switch strings.ToUpper(name) {
	case "A":
		return fmt.Sprintf("  $%02X", m.CPU.A)
	case "X":
		return fmt.Sprintf("  $%02X", m.CPU.X)
	case "Y":
		return fmt.Sprintf("  $%02X", m.CPU.Y)
	case "P":
		return fmt.Sprintf("  $%02X", m.CPU.P)
	case "SP", "S":
		return fmt.Sprintf("  $%02X", m.CPU.SP)
	case "PC":
		return fmt.Sprintf("$%04X", m.CPU.PC)
	}
	return "  ?"
}

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
		b.WriteString(fmt.Sprintf("%s %s  %02X\n", marker, dimAddr.Render(fmt.Sprintf("$%04X", sp)), m.RAM.Read(sp)))
	}
	return fitPanel("Stack", strings.TrimRight(b.String(), "\n"), w, h)
}

func (m Model) disasmView(w, h int) string {
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	rows := innerH - 1
	if rows < 1 {
		rows = 1
	}

	var lookup cpu.SymLookup
	if m.Syms != nil {
		lookup = m.Syms.Lookup
	}

	// Build a list of instruction addresses spanning a window around the
	// anchor. Anchor = PC when following; otherwise user-pinned address.
	pc := m.CPU.PC
	above := rows / 3 // keep ~1/3 of viewport above the current line
	below := rows - above - 1

	anchor := pc
	if !m.DisasmFollow {
		anchor = m.DisasmAnchor
	}

	addrs := m.cachedDisasmAddrs(anchor, above, below)

	// Render.
	var b strings.Builder
	written := 0
	for _, a := range addrs {
		if written >= rows {
			break
		}
		// Symbol label line (doesn't count as an instruction; show inline).
		if m.Syms != nil {
			if name := m.Syms.Lookup(a); name != "" {
				b.WriteString(labelStyle.Render(name+":") + "\n")
				written++
				if written >= rows {
					break
				}
			}
		}
		text, _ := cpu.DisasmWithSyms(m.RAM, a, lookup)
		if m.isDataAddr(a) {
			text = fmt.Sprintf(".byte $%02X", m.RAM.Read(a))
		}
		// Marker column: PC cursor wins, then breakpoint sigil, else blank.
		// Wide emoji (2 cells) consumes the marker slot; we drop the leading
		// space to keep the address column aligned with non-PC rows.
		var marker string
		switch {
		case a == pc:
			marker = "\U0001F449"
		default:
			if bp, ok := m.Breakpoints[a]; ok {
				marker = bp.marker()
			} else {
				marker = "  "
			}
		}
		line := fmt.Sprintf("%s %s  %s", marker, dimAddr.Render(fmt.Sprintf("$%04X", a)), text)
		if a == pc {
			line = curLine.Render(line)
		}
		b.WriteString(line + "\n")
		written++
	}
	return fitPanel("Disassembly", strings.TrimRight(b.String(), "\n"), w, h)
}

// disasmAddrsAround returns a list of instruction start addresses such that
// `pc` appears at position `above` in the list (or earlier near the bottom of
// memory). The list contains up to above+1+below entries.
//
// 6502 has variable-length instructions, so backwards disasm is approximate.
// We scan forward from pc-MaxLook with a step-of-1 candidate, and pick the
// alignment that produces the longest contiguous decode that lands exactly on
// pc. This works well for normal code; it can mis-align in data regions, but
// the worst case is a few wrong lines at the top of the window.
func disasmAddrsAround(ram cpu.Bus, pc uint16, above, below int, isData func(uint16) bool) []uint16 {
	// Walk back collecting instruction starts.
	back := walkBack(ram, pc, above)
	addrs := append([]uint16{}, back...)
	addrs = append(addrs, pc)
	// Walk forward from PC.
	cur := pc
	for i := 0; i < below; i++ {
		var step uint32
		if isData != nil && isData(cur) {
			step = 1
		} else {
			_, n := cpu.DisasmWithSyms(ram, cur, nil)
			step = uint32(n)
		}
		next := uint32(cur) + step
		if next > 0xFFFF {
			break
		}
		cur = uint16(next)
		addrs = append(addrs, cur)
	}
	return addrs
}

// walkBack returns up to `n` instruction-start addresses immediately preceding
// `pc`, in ascending order. Strategy: try every starting offset 1..MaxLook
// behind pc; the alignment that decodes cleanly all the way to pc with the
// most instructions wins.
func walkBack(ram cpu.Bus, pc uint16, n int) []uint16 {
	if n <= 0 || pc == 0 {
		return nil
	}
	const maxLook = 64 // bytes of lookback
	bestStart := pc
	bestSeq := []uint16{}
	for back := 1; back <= maxLook; back++ {
		if int(pc)-back < 0 {
			break
		}
		start := pc - uint16(back)
		// Decode forward from `start`, recording instruction addresses, until
		// we either hit pc exactly (good) or pass it (bad alignment).
		var seq []uint16
		cur := start
		ok := true
		for cur < pc {
			seq = append(seq, cur)
			_, sz := cpu.DisasmWithSyms(ram, cur, nil)
			next := uint32(cur) + uint32(sz)
			if next > uint32(pc) {
				ok = false
				break
			}
			cur = uint16(next)
		}
		if !ok || cur != pc {
			continue
		}
		// Prefer sequences with more instructions (closer to a stable boundary).
		if len(seq) > len(bestSeq) {
			bestSeq = seq
			bestStart = start
			_ = bestStart
			if len(bestSeq) >= n {
				break
			}
		}
	}
	// Trim to last n.
	if len(bestSeq) > n {
		bestSeq = bestSeq[len(bestSeq)-n:]
	}
	return bestSeq
}

// cachedDisasmAddrs wraps disasmAddrsAround with a one-entry cache. While the
// CPU is running at high Hz, this view is repainted ~60x/sec; recomputing
// walkBack each frame is wasteful when the anchor hasn't moved. The cache is
// package-level because View() takes a value receiver and can't mutate m.
func (m Model) cachedDisasmAddrs(anchor uint16, above, below int) []uint16 {
	if disasmCacheGlobal.addrs != nil &&
		disasmCacheGlobal.anchor == anchor &&
		disasmCacheGlobal.above == above &&
		disasmCacheGlobal.below == below {
		return disasmCacheGlobal.addrs
	}
	addrs := disasmAddrsAround(m.RAM, anchor, above, below, m.isDataAddr)
	disasmCacheGlobal = disasmCacheEntry{anchor: anchor, above: above, below: below, addrs: addrs}
	return addrs
}

var disasmCacheGlobal disasmCacheEntry

// isDataAddr reports whether addr is inside a known data segment from .dbg.
func (m Model) isDataAddr(addr uint16) bool {
	if _, ok := m.PCToSrc[addr]; ok {
		return false
	}
	for _, r := range m.DataRanges {
		if addr >= r.Start && addr < r.End {
			return true
		}
	}
	return false
}

// disasmScroll moves the disasm anchor by `delta` instructions (sign matters).
// Switches the panel into pinned mode so it stops following PC.
func (m *Model) disasmScroll(delta int) {
	if m.DisasmFollow {
		// First scroll: pin to current PC, then move from there.
		m.DisasmAnchor = m.CPU.PC
		m.DisasmFollow = false
	}
	a := m.DisasmAnchor
	if delta > 0 {
		for i := 0; i < delta; i++ {
			var step uint32
			if m.isDataAddr(a) {
				step = 1
			} else {
				_, sz := cpu.DisasmWithSyms(m.RAM, a, nil)
				step = uint32(sz)
			}
			next := uint32(a) + step
			if next > 0xFFFF {
				break
			}
			a = uint16(next)
		}
	} else if delta < 0 {
		// Walk back |delta| instructions using same heuristic as walkBack.
		back := walkBack(m.RAM, a, -delta)
		if len(back) > 0 {
			a = back[0]
		} else if a > 0 {
			a-- // best-effort fall-through
		}
	}
	m.DisasmAnchor = a
	m.Status = fmt.Sprintf("disasm @ $%04X (' to follow PC)", a)
}

// sourceView shows the .s file with the current PC's line highlighted.
func (m Model) sourceView(w, h int) string {
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	rows := innerH - 1
	if rows < 1 {
		rows = 1
	}

	loc, ok := m.PCToSrc[m.CPU.PC]
	if !ok {
		return fitPanel("Source", help.Render("  (no source mapping for current PC)"), w, h)
	}
	lines, ok := m.SourceFiles[loc.File]
	if !ok || len(lines) == 0 {
		return fitPanel("Source", help.Render(fmt.Sprintf("  (file unavailable: %s)", loc.File)), w, h)
	}

	// Center current line in the viewport.
	cur := loc.Line - 1 // 0-indexed
	half := rows / 2
	start := cur - half
	if start < 0 {
		start = 0
	}
	end := start + rows
	if end > len(lines) {
		end = len(lines)
		start = end - rows
		if start < 0 {
			start = 0
		}
	}

	// Per-line breakpoint lookup: first matching bp wins for marker selection.
	bpLines := map[int]*Breakpoint{}
	for pc, bp := range m.Breakpoints {
		if l, ok := m.PCToSrc[pc]; ok && l.File == loc.File {
			if _, dup := bpLines[l.Line]; !dup {
				bpLines[l.Line] = bp
			}
		}
	}

	var b strings.Builder
	title := fmt.Sprintf("Source — %s:%d", loc.File, loc.Line)
	for i := start; i < end; i++ {
		lineNum := i + 1
		var marker string
		switch {
		case lineNum == loc.Line:
			marker = "\U0001F449"
		default:
			if bp, ok := bpLines[lineNum]; ok {
				marker = bp.marker()
			} else {
				marker = "  "
			}
		}
		text := fmt.Sprintf("%s %s  %s", marker, dimAddr.Render(fmt.Sprintf("%4d", lineNum)), lines[i])
		if lineNum == loc.Line {
			text = curLine.Render(text)
		}
		b.WriteString(text + "\n")
	}
	return fitPanel(title, strings.TrimRight(b.String(), "\n"), w, h)
}

func (m Model) memView(w, h int) string {
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	rows := innerH - 1
	if rows < 1 {
		rows = 1
	}

	addr := m.MemViewAddr & 0xFFF0
	var b strings.Builder
	for row := 0; row < rows; row++ {
		base := addr + uint16(row*16)
		b.WriteString(dimAddr.Render(fmt.Sprintf("$%04X:", base)))
		var ascii strings.Builder
		for col := 0; col < 16; col++ {
			a := base + uint16(col)
			v := m.RAM.Read(a)
			cell := fmt.Sprintf(" %02X", v)
			if mbp, ok := m.MemBPs[a]; ok && mbp != nil {
				switch mbp.Kind {
				case MemBPRead:
					cell = " " + memBPRead.Render(fmt.Sprintf("%02X", v))
				case MemBPWrite:
					cell = " " + memBPWrite.Render(fmt.Sprintf("%02X", v))
				case MemBPReadWrite:
					cell = " " + memBPRW.Render(fmt.Sprintf("%02X", v))
				}
			}
			b.WriteString(cell)
			if v >= 32 && v < 127 {
				ascii.WriteByte(v)
			} else {
				ascii.WriteByte('.')
			}
		}
		b.WriteString("  " + ascii.String() + "\n")
	}
	hint := help.Render("  (j/k ±$10  J/K ±$100  g/G top/bot)")
	return fitPanel("Memory"+hint, strings.TrimRight(b.String(), "\n"), w, h)
}
