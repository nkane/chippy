package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/peripheral"
	"github.com/nkane/chippy/internal/symbols"
)

// Theme-driven styles. applyTheme() in theme.go reassigns these; the
// init below picks default until New(c, r) overwrites it from the
// persisted state (or the CLI `--theme` flag).
var (
	titleStyle lipgloss.Style
	panelStyle lipgloss.Style
	regStyle   lipgloss.Style
	flagOn     lipgloss.Style
	flagOff    lipgloss.Style
	curLine    lipgloss.Style
	help       lipgloss.Style
	statusBar  lipgloss.Style
	labelStyle lipgloss.Style
	dimAddr    lipgloss.Style
	memBPRead  lipgloss.Style
	memBPWrite lipgloss.Style
	memBPRW    lipgloss.Style
	memCursor  lipgloss.Style
	memEdit    lipgloss.Style
)

func init() { applyTheme(ThemeDefault) }

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
	HelpPage int // current page of the multi-page help modal
	ShowBPs  bool
	BPCursor int // selected row in BP manager

	// Source view
	ShowSource  bool                      // toggle source panel vs disassembly
	SourceFiles map[string][]string       // filename -> lines (1-indexed via [i-1])
	PCToSrc     map[uint16]symbols.SrcLoc // PC -> (file, line)
	DataRanges  []symbols.Range           // [start,end) regions to render as .byte

	// Watch list
	Watches []Watch

	// Run-speed control: target Hz (0 = unthrottled).
	TargetHz  int
	lastRunNS int64
	cycleDebt int64

	// Command prompt ( `:` line editor )
	PromptActive bool
	PromptBuf    string

	// Prompt command history. Persisted to HistPath when set, capped at
	// histCap entries. HistIdx walks back from the newest entry; -1 means
	// "not navigating, PromptBuf is live". HistTemp saves the in-progress
	// buffer when the user starts walking with Up so Down restores it.
	History  []string
	HistPath string
	HistIdx  int
	HistTemp string

	// Reverse-incremental search (Ctrl+R) sub-state. When RISearchActive,
	// keystrokes feed RISearchBuf, PromptBuf shows the matched entry, and
	// RIOrigBuf is restored on Esc. RIMatchIdx is the History index of
	// the current match or -1 if none.
	RISearchActive bool
	RISearchBuf    string
	RIOrigBuf      string
	RIMatchIdx     int

	// State persistence
	StatePath string

	// Memory-mapped peripherals (optional). When set, the View grows an
	// Output panel and the `i` key toggles InputMode: while active, all
	// keystrokes are routed to Keyboard.Push instead of debugger bindings,
	// and Esc exits input mode.
	TextOut   *peripheral.TextOutput
	Keyboard  *peripheral.KeyboardInput
	InputMode bool

	// Optional CPU execution tracer. The CPU does the actual logging; the
	// Model holds a reference so `:trace on/off [path]` can flip it at
	// runtime and so quit can flush the buffer.
	Tracer *cpu.FileTracer

	// StackAnnotate controls the Stack panel's render mode. true (default):
	// JSR return-address pairs are detected and shown as `ret $XXXX` rows
	// with callee + source info; consecutive non-frame bytes are collapsed.
	// false: legacy one-byte-per-row layout. `T` key toggles.
	StackAnnotate bool

	// Memory-panel byte cursor. MemViewAddr is the row-anchored top-left
	// of the panel; MemCursor is the byte highlighted within (any address,
	// not row-aligned). Arrow keys move the cursor; `e` enters edit mode.
	MemCursor  uint16
	MemEditing bool
	MemEditBuf string

	// Reverse-step ring. Each explicit step (s/n/f and their loops) pushes
	// a pre-step snapshot here so `<` can rewind. Free-run via tickMsg does
	// NOT push — 64 KiB per snapshot at multi-MHz throughput would dominate
	// the runtime cost. Nil disables the feature; default cap is set in New.
	Rewind *rewindRing

	// Immediate window — a modal REPL over the chippy expression grammar.
	// `I` opens, Esc closes; while open, all keystrokes feed
	// updateImmediate. Each Enter compiles + evaluates the buffer against
	// current CPU state and appends `{Expr, Result}` to ImmediateHistory.
	ImmediateActive  bool
	ImmediateBuf     string
	ImmediateHistory []ImmediateEntry

	// Disassembly viewport: when DisasmFollow is true (default), the panel
	// re-anchors on PC each frame. User scroll keys flip it off and pin
	// DisasmAnchor as the address shown at the top of the window.
	DisasmFollow bool
	DisasmAnchor uint16

	// Theme: the active color palette name. Empty/unknown resolves to
	// default. NO_COLOR env always forces mono regardless. Persisted
	// alongside the rest of the savedState fields.
	Theme string

	W, H int
}

type disasmCacheEntry struct {
	anchor       uint16
	above, below int
	addrs        []uint16
}

func New(c *cpu.CPU, r *cpu.RAM) Model {
	r.EnableShadow() // CoW page tracking powers the rewind ring (issue #66).
	// Pick theme from NO_COLOR env at start; CLI / state-file overrides
	// land later via WithTheme / loadState.
	t := resolveTheme(string(ThemeDefault))
	applyTheme(t)
	return Model{
		CPU:           c,
		RAM:           r,
		Breakpoints:   map[uint16]*Breakpoint{},
		MemBPs:        map[uint16]*MemBP{},
		MemViewAddr:   0x0000,
		Status:        "ready",
		TargetHz:      0,
		DisasmFollow:  true,
		StackAnnotate: true,
		HistIdx:       -1,
		RIMatchIdx:    -1,
		Rewind:        newRewindRing(defaultRewindCap),
		Theme:         string(t),
		W:             120,
		H:             40,
	}
}

// WithTheme overrides the theme picked by New. Used by the CLI's
// --theme flag. Empty string keeps whatever New chose.
func (m Model) WithTheme(name string) Model {
	if name == "" {
		return m
	}
	t := resolveTheme(name)
	applyTheme(t)
	m.Theme = string(t)
	return m
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

// WithTextOutput attaches a TextOutput peripheral so the TUI can render its
// buffered output. The peripheral must already be registered with the MMIO
// bus the CPU reads/writes through — this method only wires display.
func (m Model) WithTextOutput(t *peripheral.TextOutput) Model {
	m.TextOut = t
	return m
}

// WithKeyboard attaches a KeyboardInput peripheral so the TUI can route
// keystrokes into the program (see InputMode).
func (m Model) WithKeyboard(k *peripheral.KeyboardInput) Model {
	m.Keyboard = k
	return m
}

// WithTracer attaches a CPU execution tracer for runtime control via :trace.
func (m Model) WithTracer(t *cpu.FileTracer) Model {
	m.Tracer = t
	return m
}

// WithHistoryPath enables persistent prompt history at path (typically
// tui.DefaultHistoryPath()). Loads existing history on attach; the prompt
// auto-saves on every committed command.
func (m Model) WithHistoryPath(p string) Model {
	m.HistPath = p
	m.History = loadHistory(p)
	return m
}

// WithRunOnStart starts the CPU running immediately instead of paused.
// Useful with -trace for non-interactive capture sessions where there's no
// user to press `r`.
func (m Model) WithRunOnStart(run bool) Model {
	if run {
		m.Running = true
		m.Status = "running"
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
		// Help modal: paging keys advance, any other key dismisses.
		if m.ShowHelp {
			switch msg.String() {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "space", "n", "right", "pgdown", "tab", "j", "down":
				m.HelpPage = (m.HelpPage + 1) % helpPageCount()
				return m, m.scheduleTick()
			case "p", "left", "pgup", "shift+tab", "k", "up":
				m.HelpPage = (m.HelpPage - 1 + helpPageCount()) % helpPageCount()
				return m, m.scheduleTick()
			}
			m.ShowHelp = false
			m.HelpPage = 0
			return m, m.scheduleTick()
		}
		// BP manager modal.
		if m.ShowBPs {
			return m.updateBPManager(msg)
		}
		// Input mode: route most keys to the keyboard peripheral instead
		// of debugger bindings. Ctrl+C always quits; Esc exits the mode.
		if m.InputMode {
			return m.updateInputMode(msg)
		}
		// Memory editor owns input while open.
		if m.MemEditing {
			return m.updateMemEdit(msg)
		}
		// Immediate window owns input while open.
		if m.ImmediateActive {
			return m.updateImmediate(msg)
		}

		switch msg.String() {
		case "q", "ctrl+c":
			m.saveState()
			return m, tea.Quit
		case "i":
			if m.Keyboard != nil {
				m.InputMode = true
				m.Status = "input mode — Esc to exit"
			} else {
				m.Status = "no keyboard peripheral registered"
			}
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
				m.step()
				m.statusAfterStep("stepped")
			}
		case "S":
			if m.CPU.Halted {
				m.Status = "halted (press R to reset)"
				break
			}
			for i := 0; i < 16 && !m.CPU.Halted; i++ {
				m.step()
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
			if m.Rewind != nil {
				m.Rewind.Reset()
			}
			m.Status = "reset"
		case "<":
			if m.Rewind == nil || m.Rewind.Len() == 0 {
				m.Status = "rewind: empty"
				break
			}
			s, _ := m.Rewind.Pop()
			m.CPU.Restore(s, m.RAM)
			m.restoreperipherals(s)
			m.Status = fmt.Sprintf("rewind -> $%04X (depth %d)", m.CPU.PC, m.Rewind.Len())
		case "I":
			m.ImmediateActive = true
			m.ImmediateBuf = ""
			m.Status = "immediate window"
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
		case "j":
			m.MemViewAddr += 0x10
		case "k":
			m.MemViewAddr -= 0x10
		case "J", "pgdown":
			m.MemViewAddr += 0x100
		case "K", "pgup":
			m.MemViewAddr -= 0x100
		case "g":
			m.MemViewAddr = 0
		case "G":
			m.MemViewAddr = 0xFF00
		case "left":
			m.MemCursor--
			m.memCursorMoved()
		case "right":
			m.MemCursor++
			m.memCursorMoved()
		case "up":
			m.MemCursor -= 0x10
			m.memCursorMoved()
		case "down":
			m.MemCursor += 0x10
			m.memCursorMoved()
		case "e":
			m.MemEditing = true
			m.MemEditBuf = ""
			m.Status = fmt.Sprintf("edit $%04X (hex; enter=commit esc=cancel)", m.MemCursor)
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
		case "T":
			m.StackAnnotate = !m.StackAnnotate
			if m.StackAnnotate {
				m.Status = "stack: annotated"
			} else {
				m.Status = "stack: raw bytes"
			}
		}
		return m, m.scheduleTick()

	case tickMsg:
		if m.Running {
			budget := m.runBudget()
			for i := 0; i < budget; i++ {
				m.step()
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

// updateInputMode routes keystrokes to the keyboard peripheral. Esc exits;
// Ctrl+C still quits. Printable ASCII and a small set of control keys are
// mapped to bytes; everything else is silently dropped so an unmapped
// special key (e.g. a function key) doesn't poison the program input.
func (m Model) updateInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := msg.String()
	switch s {
	case "ctrl+c":
		m.saveState()
		return m, tea.Quit
	case "esc":
		m.InputMode = false
		m.Status = "input mode off"
		return m, m.scheduleTick()
	}
	if b, ok := keyMsgToByte(msg); ok {
		m.Keyboard.Push(b)
		m.Status = fmt.Sprintf("key -> $%02X", b)
	}
	return m, m.scheduleTick()
}

// keyMsgToByte maps a Bubble Tea key event to the byte the 6502 program
// will see. Returns ok=false for keys that have no useful mapping (function
// keys, modifier-only chords, etc.).
func keyMsgToByte(msg tea.KeyMsg) (byte, bool) {
	s := msg.String()
	switch s {
	case "enter":
		return 0x0D, true
	case "tab":
		return 0x09, true
	case "space":
		return 0x20, true
	case "backspace":
		return 0x08, true
	}
	// Single-rune printable.
	if len(s) == 1 {
		r := s[0]
		if r >= 0x20 && r < 0x7F {
			return r, true
		}
	}
	return 0, false
}

// step takes one CPU instruction *and* records a rewind snapshot so `<`
// can undo it. Use this in all explicit-step keypaths (`s` / `S` / `n` /
// `f` and the inner loops of stepOver / runToNextLine). Snapshots are
// page-level CoW deltas (issue #66) — typical cost is hundreds of bytes,
// so the tickMsg free-run loop snapshots per step too.
func (m *Model) step() int {
	if m.Rewind == nil {
		return m.CPU.Step()
	}
	snap := m.CPU.Snapshot(m.RAM)
	m.captureperipherals(&snap)
	m.RAM.ResetShadow()
	n := m.CPU.Step()
	snap.Pages = m.RAM.TakeShadow()
	m.Rewind.Push(snap)
	return n
}

// captureperipherals fills the snapshot's Peripherals map with the
// current state of every wired MMIO device. Keys are the peripheral's
// base MMIO address as `"$XXXX"` so restore can route bytes back to
// the right device.
func (m *Model) captureperipherals(s *cpu.Snapshot) {
	if m.TextOut == nil && m.Keyboard == nil {
		return
	}
	s.Peripherals = map[string][]byte{}
	if m.TextOut != nil {
		s.Peripherals[fmt.Sprintf("$%04X", m.TextOut.Addr)] = m.TextOut.Snapshot()
	}
	if m.Keyboard != nil {
		s.Peripherals[fmt.Sprintf("$%04X", m.Keyboard.DataAddr)] = m.Keyboard.Snapshot()
	}
}

// restoreperipherals applies a snapshot's Peripherals map back to the
// wired devices. Missing keys leave the corresponding device untouched.
func (m *Model) restoreperipherals(s cpu.Snapshot) {
	if s.Peripherals == nil {
		return
	}
	if m.TextOut != nil {
		if state, ok := s.Peripherals[fmt.Sprintf("$%04X", m.TextOut.Addr)]; ok {
			m.TextOut.Restore(state)
		}
	}
	if m.Keyboard != nil {
		if state, ok := s.Peripherals[fmt.Sprintf("$%04X", m.Keyboard.DataAddr)]; ok {
			m.Keyboard.Restore(state)
		}
	}
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
		m.step()
		m.statusAfterStep("stepped")
		return
	}
	retPC := m.CPU.PC + 3
	const guard = 2_000_000
	for i := 0; i < guard; i++ {
		m.step()
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
		m.step()
		m.statusAfterStep("stepped (no src map)")
		return
	}
	const guard = 1_000_000
	for i := 0; i < guard; i++ {
		m.step()
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

	outH := 0
	if m.TextOut != nil {
		outH = 8
	}
	disH := innerH * 6 / 10
	if disH < 10 {
		disH = 10
	}
	memH := innerH - disH - outH
	if memH < 8 {
		memH = 8
		disH = innerH - memH - outH
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
	rightPanels := []string{topRight, m.memView(rightW, memH)}
	if m.TextOut != nil {
		rightPanels = append(rightPanels, m.outputView(rightW, outH))
	}
	right := lipgloss.JoinVertical(lipgloss.Left, rightPanels...)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, strings.Repeat(" ", gap), right)

	title := titleStyle.Render(" chippy — 6502 TUI debugger ")
	titleRow := lipgloss.PlaceHorizontal(m.W, lipgloss.Center, title)

	rewindSeg := ""
	if d := m.Rewind.Len(); d > 0 {
		rewindSeg = fmt.Sprintf(" │ rwd:%d", d)
	}
	statusText := fmt.Sprintf(
		" %s │ cyc=%d │ PC=$%04X │ %s%s │ [?] help  [:] cmd  [s/n] step  [r] run  [<] back  [v] src  [q] quit",
		m.Status, m.CPU.Cycles, m.CPU.PC, speedLabel(m.TargetHz), rewindSeg,
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
	case m.ImmediateActive:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.immediateModal())
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

// helpSection is one labeled group of key->description rows.
type helpSection struct {
	title string
	rows  [][2]string
}

// helpPages partitions the keybinding reference into pages so the modal
// fits on small terminals. Grow within the current scheme: each page should
// be ~3 sections / ~12 rows max so a 24-row terminal still renders cleanly.
func helpPages() [][]helpSection {
	return [][]helpSection{
		// Page 1 — core debugger controls
		{
			{"Execution", [][2]string{
				{"s", "step one instruction"},
				{"S", "step 16 instructions"},
				{"n", "step over (run JSR to RTS)"},
				{"f", "run to next source line"},
				{"<", "rewind one step (snapshot ring; depth shown as `rwd:N`)"},
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
			{"General", [][2]string{
				{":", "command line — see Prompt verbs page for the full list"},
				{"v", "toggle source / disassembly view"},
				{"i", "input mode → keystrokes go to keyboard peripheral (Esc exits)"},
				{"? / h", "toggle this help"},
				{"q / ^C", "quit"},
			}},
			{"Prompt verbs", [][2]string{
				{":goto X", "scroll memory pane to addr/symbol"},
				{":pc X", "set CPU PC"},
				{":run X", "run until addr (one-shot bp + go)"},
				{":watch X [byte|word] [label]", "add value watch (also :watch reg <A|X|Y|P|SP|PC>)"},
				{":rmwatch X", "remove a watch (also :rmwatch reg <name>)"},
				{":clearwatch", "remove ALL watches"},
				{":speed Hz", "throttle to Hz (0 = max; try :speed 60)"},
			}},
		},
		// Page 2 — panels
		{
			{"Memory view", [][2]string{
				{"j / k", "scroll down / up by $10"},
				{"J / PgDn", "scroll down by $100"},
				{"K / PgUp", "scroll up by $100"},
				{"g / G", "jump to $0000 / $FF00"},
				{"← →", "move byte cursor ±1"},
				{"↑ ↓", "move byte cursor ±$10 (auto-scrolls)"},
				{"e", "edit byte at cursor (hex; enter=commit esc=cancel)"},
			}},
			{"Disassembly", [][2]string{
				{"[ / ]", "scroll one instruction up / down"},
				{"{ / }", "scroll eight instructions up / down"},
				{"'", "follow PC again"},
			}},
			{"Stack panel", [][2]string{
				{"T", "toggle JSR-frame annotation / raw bytes"},
				{"annotated", "ret $XXXX + callee + source line per JSR pair"},
				{"raw", "one byte per row from SP up"},
			}},
		},
		// Page 3 — prompt + breakpoint commands
		{
			{"Prompt", [][2]string{
				{"↑ / ↓", "walk command history (persisted to ~/.chippy/history)"},
				{"Tab", "complete verb or symbol (after :bp etc.)"},
				{"Ctrl-R", "reverse-incremental search history (Ctrl-R again = next)"},
				{"Esc", "cancel prompt or RI search"},
			}},
			{"Immediate window", [][2]string{
				{"I", "open expression REPL"},
				{"<expr>", "evaluate against CPU state (A+X, [$0200], PC>=main, …)"},
				{"↑", "recall last expression"},
				{"Esc", "close"},
			}},
			{"Breakpoints", [][2]string{
				{":bp X", "toggle plain bp at addr/symbol/file.s:42"},
				{":bp X once", "one-shot (auto-delete on hit)"},
				{":bp X hits N", "break only on Nth hit"},
				{":bp X if E", "conditional (E uses A,X,Y,P,SP,PC,N,V,Z,C,[$XX])"},
				{":bp X log M", "log point: prints M, doesn't pause"},
				{"sigils", "🛑 plain  🔶 cond  📜 log  💩 reject  👉 PC"},
			}},
		},
		// Page 4 — memory watchpoints + trace
		{
			{"Memory watchpoints", [][2]string{
				{":bpr X", "watch reads at X"},
				{":bpw X", "watch writes at X"},
				{":bprw X", "watch both reads and writes"},
				{":rmbpr X", "remove (also :rmbpw / :rmbprw)"},
				{"modifiers", "same: once / hits N / if E / log M"},
				{"sigils", "👁 read  ✏ write  🔁 read+write"},
			}},
			{"Trace", [][2]string{
				{":trace PATH", "open PATH and enable per-instruction trace"},
				{":trace on", "re-enable using the last-set path"},
				{":trace off", "disable + flush buffer to disk"},
				{":trace", "show current state"},
				{"--trace", "CLI flag: enable at startup with given path"},
				{"--run-on-start", "start running immediately (pair with --trace)"},
			}},
			{"Text output ($F001)", [][2]string{
				{":textsave PATH", "dump TextOutput buffer to a file"},
				{"--text-buf-cap N", "TextOutput buffer cap in bytes (default 64 KiB; 0 = unbounded)"},
			}},
			{"Theme", [][2]string{
				{":theme", "show current palette"},
				{":theme NAME", "switch palette: default | mono | protan | tritan"},
				{"--theme NAME", "CLI flag — pick the palette at startup"},
				{"NO_COLOR=1", "env var: force mono regardless of theme"},
			}},
		},
	}
}

func helpPageCount() int { return len(helpPages()) }

func (m Model) helpModal() string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	sectStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("207")).Bold(true).Underline(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Italic(true)

	row := func(k, d string) string {
		return fmt.Sprintf("  %s   %s", keyStyle.Render(fmt.Sprintf("%-10s", k)), descStyle.Render(d))
	}

	pages := helpPages()
	pageIdx := m.HelpPage
	if pageIdx < 0 || pageIdx >= len(pages) {
		pageIdx = 0
	}
	sections := pages[pageIdx]

	var b strings.Builder
	b.WriteString(titleStyle.Render(fmt.Sprintf("chippy — keybindings  (page %d/%d)", pageIdx+1, len(pages))))
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
	b.WriteString(dim.Render("  space/→: next page   p/←: prev   any other key: close"))

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
		fmt.Fprintf(&b, "%s %s\n",
			dimAddr.Render(fmt.Sprintf("%-10s", name)),
			lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true).Render(val))
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
		text, _ := cpu.DisasmCPUWithSyms(m.CPU, a, lookup)
		if m.isDataAddr(a) {
			text = fmt.Sprintf(".byte $%02X", m.RAM.Read(a))
		}
		// Marker column: PC cursor wins, then breakpoint sigil, else blank.
		// Wide emoji (2 cells) consumes the marker slot; we drop the leading
		// space to keep the address column aligned with non-PC rows.
		var marker string
		switch a {
		case pc:
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
func disasmAddrsAround(c *cpu.CPU, pc uint16, above, below int, isData func(uint16) bool) []uint16 {
	// Walk back collecting instruction starts.
	back := walkBack(c, pc, above)
	addrs := append([]uint16{}, back...)
	addrs = append(addrs, pc)
	// Walk forward from PC.
	cur := pc
	for i := 0; i < below; i++ {
		var step uint32
		if isData != nil && isData(cur) {
			step = 1
		} else {
			_, n := cpu.DisasmCPUWithSyms(c, cur, nil)
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
// walkBack is a thin wrapper around cpu.WalkBack — kept so existing call
// sites (in this file and disasmAddrsAround) don't need a per-call
// package prefix. The actual heuristic lives in internal/cpu so the DAP
// server's disassemble handler can share it.
func walkBack(c *cpu.CPU, pc uint16, n int) []uint16 {
	return cpu.WalkBack(c, pc, n)
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
	addrs := disasmAddrsAround(m.CPU, anchor, above, below, m.isDataAddr)
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
		back := walkBack(m.CPU, a, -delta)
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
// nearestSrcLocBelow finds the highest mapped PC that is ≤ target and
// returns its SrcLoc. Used by sourceView to fall back to "the last C
// line we knew about" while stepping through unmapped generated code
// (cc65 runtime stubs, etc.) so the panel doesn't go blank mid-step.
func (m Model) nearestSrcLocBelow(target uint16) (symbols.SrcLoc, bool) {
	if m.PCToSrc == nil {
		return symbols.SrcLoc{}, false
	}
	const window = 0x400 // 1 KiB lookback; covers a typical cc65 runtime helper chain
	best := uint16(0)
	have := false
	for offset := uint16(0); offset < window; offset++ {
		if uint16(target) < offset {
			break
		}
		pc := target - offset
		if _, ok := m.PCToSrc[pc]; ok {
			best = pc
			have = true
			break
		}
	}
	if !have {
		return symbols.SrcLoc{}, false
	}
	return m.PCToSrc[best], true
}

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
	// Fallback: cc65 runtime stubs (pusha, copydata, etc.) have no
	// source mapping in the .dbg. Rather than blanking the panel mid-
	// step, find the nearest mapped PC at or below the current one and
	// show its source — with a hint that we're inside generated code.
	inGenerated := false
	if !ok {
		if near, nok := m.nearestSrcLocBelow(m.CPU.PC); nok {
			loc = near
			ok = true
			inGenerated = true
		}
	}
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
	if inGenerated {
		title = fmt.Sprintf("Source — %s:%d  (PC $%04X in generated code)", loc.File, loc.Line, m.CPU.PC)
	}
	for i := start; i < end; i++ {
		lineNum := i + 1
		var marker string
		switch lineNum {
		case loc.Line:
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

// outputView renders the TextOutput peripheral's buffer. The newest content
// is shown — older lines scroll off the top when the buffer overflows the
// panel height.
func (m Model) outputView(w, h int) string {
	innerH := h - 2
	if innerH < 1 {
		innerH = 1
	}
	rows := innerH - 1
	if rows < 1 {
		rows = 1
	}
	innerW := w - panelWChrome
	if innerW < 1 {
		innerW = 1
	}

	out := m.TextOut.String()
	lines := strings.Split(out, "\n")
	// Wrap any line wider than the panel.
	wrapped := make([]string, 0, len(lines))
	for _, ln := range lines {
		if len(ln) <= innerW {
			wrapped = append(wrapped, ln)
			continue
		}
		for len(ln) > innerW {
			wrapped = append(wrapped, ln[:innerW])
			ln = ln[innerW:]
		}
		wrapped = append(wrapped, ln)
	}
	// Trim to last `rows` lines.
	if len(wrapped) > rows {
		wrapped = wrapped[len(wrapped)-rows:]
	}

	title := "Output"
	if m.InputMode {
		title = "Output  " + lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render("[INPUT]")
	}
	return fitPanel(title, strings.Join(wrapped, "\n"), w, h)
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
			byteStr := fmt.Sprintf("%02X", v)
			cell := " " + byteStr

			if mbp, ok := m.MemBPs[a]; ok && mbp != nil {
				switch mbp.Kind {
				case MemBPRead:
					cell = " " + memBPRead.Render(byteStr)
				case MemBPWrite:
					cell = " " + memBPWrite.Render(byteStr)
				case MemBPReadWrite:
					cell = " " + memBPRW.Render(byteStr)
				}
			}
			if a == m.MemCursor {
				switch {
				case m.MemEditing:
					buf := m.MemEditBuf
					switch len(buf) {
					case 0:
						buf = "__"
					case 1:
						buf = "_" + buf
					}
					cell = " " + memEdit.Render(buf)
				default:
					cell = " " + memCursor.Render(byteStr)
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
	hint := help.Render(fmt.Sprintf("  (cur $%04X  arrows move  e edit)", m.MemCursor))
	return fitPanel("Memory"+hint, strings.TrimRight(b.String(), "\n"), w, h)
}
