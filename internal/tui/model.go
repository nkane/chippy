package tui

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/dap"
	"github.com/nkane/chippy/peripheral"
	"github.com/nkane/chippy/symbols"
	"github.com/nkane/chippy/trace"
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

// dapEventMsg wraps a single DAP event from the RemoteSource for the
// TUI's Update loop. Emitted by waitForDAPEvent.
type dapEventMsg struct {
	ev dap.Event
}

// dapClosedMsg fires when the RemoteSource's event channel closes (the
// underlying DAP connection ended). The TUI treats this as a graceful
// shutdown signal.
type dapClosedMsg struct{}

// waitForDAPEvent returns a tea.Cmd that blocks on the source's event
// channel and emits a dapEventMsg / dapClosedMsg. The Update handler
// re-schedules itself after consuming each event so the loop continues
// for the program's lifetime.
func waitForDAPEvent(src Source) tea.Cmd {
	if src == nil {
		return nil
	}
	ch := src.Events()
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return dapClosedMsg{}
		}
		return dapEventMsg{ev: ev}
	}
}

// Watch is a pinned memory address shown in the watch panel.
type Watch struct {
	// Kind is "mem" (default) or "reg". Empty string treated as "mem" for
	// backward compat with state files written before registers existed.
	Kind  string `json:"kind,omitempty"`
	Addr  uint16 `json:"addr,omitempty"`
	Label string `json:"label,omitempty"`
	Width int    `json:"width,omitempty"` // mem: 1 = byte, 2 = word (LE)
	Reg   string `json:"reg,omitempty"`   // reg: A,X,Y,P,SP,PC
	// Count > 1 marks an array watch: Count consecutive elements of Width
	// bytes each, starting at Addr, rendered as name[0..Count-1]. cc65 .dbg
	// rarely carries array bounds for data symbols, so this is usually set
	// from an explicit `xN` token on `:watch`; when a sym `size=` is present
	// it seeds the default.
	Count int `json:"count,omitempty"`
	// Fields, when non-empty, marks a struct-overlay watch: each member is
	// rendered as a named row at Addr+Offset. cc65 .dbg carries no struct
	// member layout (V2.18 collapses all csym types to void), so the layout
	// is user-declared via `:watch X as {field:width, ...}` (issue #409).
	// A struct watch takes precedence over Count.
	Fields []WatchField `json:"fields,omitempty"`
}

// WatchField is one member of a struct-overlay watch (issue #409).
type WatchField struct {
	Name   string `json:"name,omitempty"`
	Offset int    `json:"offset,omitempty"` // byte offset from the watch Addr
	Width  int    `json:"width,omitempty"`  // 1 = byte, 2 = word (LE)
}

type Model struct {
	CPU  *cpu.CPU
	RAM  *cpu.RAM
	WBus *WBus // optional: bus wrapper that records mem watch hits

	// Source abstracts the step / reset / breakpoint / rewind control
	// paths so the TUI can drive either a local in-process CPU
	// (LocalSource — default) or a remote DAP-backed CPU
	// (RemoteSource — used by `chippy -dap-attach`). Display panels
	// continue to read CPU + RAM fields directly because the source
	// keeps them populated as a mirror in both modes.
	Source Source

	// Regs is the register snapshot the Registers panel renders, refreshed
	// from the Source via a DAP `variables` round-trip (issue #394) — the
	// panel no longer reads cpu.CPU fields directly.
	Regs RegSnapshot

	// Stack is the stack-page frame snapshot the Stack panel renders,
	// refreshed from the Source via a DAP `stackTrace` round-trip (issue
	// #449) — the panel no longer runs DetectStackFrame / symbol lookups.
	Stack StackSnapshot

	// Flags is the decomposed P-register snapshot the Flags panel renders,
	// refreshed from the Source via a DAP `variables` (Flags scope) round-trip
	// (issue #450) — the panel no longer bit-tests cpu.CPU.P directly.
	Flags FlagsSnapshot

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

	// Symbols modal — paginated browser over the loaded `.dbg`
	// label table. Opened via `:syms` (optionally `:syms PREFIX`
	// for a pre-filter). Enter on a row toggles a breakpoint at
	// that symbol. Esc / q closes.
	ShowSyms   bool
	SymsCursor int    // selected row
	SymsFilter string // optional substring filter
	SymsOffset int    // scroll offset within the filtered list

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

	// Quake-style drop-down console (issue #232 draft). Backtick
	// toggles. While active a scrollback panel covers the top ~50%
	// of the screen with an embedded prompt — each Enter runs the
	// current buffer through runCommand and appends `> cmd` + the
	// result to the scrollback. Esc / backtick closes; PgUp / PgDn
	// scroll. The existing `:` prompt stays as the bottom-line
	// quick-fire variant.
	ConsoleActive       bool
	ConsoleBuf          string
	ConsoleScrollback   []string
	ConsoleScrollOffset int

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

	// Deep rewind (issue #392). StepCount counts every executed step since
	// the last reset. Keyframes holds periodic full-RAM snapshots (one every
	// keyframeInterval steps) so `:rewind N` can reconstruct a state far
	// beyond the fine ring's depth by restoring the nearest keyframe and
	// replaying forward. RewindBudgetMB caps keyframe memory; the ring drops
	// the oldest keyframe when full, so reach = budget/64KiB × interval.
	StepCount       uint64
	Keyframes       *cpu.KeyframeRing
	RewindBudgetMB  int
	replayingRewind bool // suppresses keyframe capture during forward replay

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

	// Source viewport: same pattern as disasm. SourceFollow=true (default)
	// re-centers on the current PC's source line every frame. User scroll
	// keys flip it off and pin SourceAnchorFile + SourceAnchorLine as the
	// centered row. ' restores follow mode.
	SourceFollow     bool
	SourceAnchorFile string
	SourceAnchorLine int

	// Theme: the active color palette name. Empty/unknown resolves to
	// default. NO_COLOR env always forces mono regardless. Persisted
	// alongside the rest of the savedState fields.
	Theme string

	// CPUMu (optional) — shared with a co-running DAP server when the
	// user has typed `:dap PORT`. nil otherwise. The TUI takes the
	// mutex around every CPU / RAM / peripheral mutation so the editor
	// driving the DAP session doesn't race.
	CPUMu *sync.Mutex

	// SrcMap is the raw .dbg source-map pointer. Held separately from
	// the flattened PCToSrc / SourceFiles / DataRanges so the DAP
	// attach path can hand the live object to the embedded server.
	SrcMap *symbols.SourceMap

	// DAPListenAddr is the TCP address the embedded DAP server is
	// listening on after `:dap PORT`. Empty otherwise. Surfaced in the
	// status bar so users can paste it into their editor's attach
	// config.
	DAPListenAddr string

	// TraceReplay (optional) — when non-nil, step keys scroll through
	// a pre-recorded execution trace instead of advancing the live
	// CPU. Issue #64. The CPU's regs are kept in sync with the
	// current frame so every panel reads as if the CPU were paused at
	// that PC.
	TraceReplay *trace.Replay

	// lastFind is the most recent `:find` expression; a bare `:find` /
	// `:rfind` repeats it so users can sweep through every match.
	lastFind string

	// ReplayDiff (optional) is a second trace loaded via `-diff`. When set,
	// the replay view renders both traces side by side and Diverge marks the
	// first frame where they disagree. Issue #391.
	ReplayDiff *trace.Replay
	Diverge    trace.Divergence
	ShowDiff   bool // diff overlay open (toggled with `d`)

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
	rewind := newRewindRing(defaultRewindCap)
	m := Model{
		CPU:            c,
		RAM:            r,
		Breakpoints:    map[uint16]*Breakpoint{},
		MemBPs:         map[uint16]*MemBP{},
		MemViewAddr:    0x0000,
		Status:         "ready",
		TargetHz:       0,
		DisasmFollow:   true,
		SourceFollow:   true,
		StackAnnotate:  true,
		HistIdx:        -1,
		RIMatchIdx:     -1,
		Rewind:         rewind,
		RewindBudgetMB: defaultRewindBudgetMB,
		Keyframes:      cpu.NewKeyframeRing(defaultRewindBudgetMB << 20),
		Theme:          string(t),
		W:              120,
		H:              40,
	}
	m.Source = NewLocalSource(c, r)
	m.syncRegs()  // seed the Registers panel before the first render
	m.syncStack() // seed the Stack panel before the first render
	m.syncFlags() // seed the Flags panel before the first render
	return m
}

// WithSource replaces the default LocalSource with a custom source —
// typically a *RemoteSource built by cmd/chippy -dap-attach. The
// Model's CPU + RAM remain in place as the display mirror; the source
// is expected to keep them populated.
func (m Model) WithSource(s Source) Model {
	m.Source = s
	m.syncRegs()
	m.syncStack()
	m.syncFlags()
	return m
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
	// Push the table into the in-process DAP server so local-mode stackTrace
	// frames carry callee names (issue #449); refresh the cached snapshot.
	if ls, ok := m.Source.(*LocalSource); ok {
		ls.SetSymbols(s, nil)
		m.syncStack()
	}
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

// WithTraceReplay attaches a pre-parsed Replay so step keys scroll
// through recorded frames instead of running the live CPU. Issue #64.
// The first frame's regs are copied to the CPU so initial render
// matches the recording.
func (m Model) WithTraceReplay(r *trace.Replay) Model {
	if r == nil || r.Len() == 0 {
		return m
	}
	m.TraceReplay = r
	m.applyTraceFrame()
	m.Status = fmt.Sprintf("trace replay (frame 1/%d)", r.Len())
	return m
}

// WithReplayDiff attaches a second trace for side-by-side diffing against
// the primary replay (issue #391). Computes the first divergence eagerly so
// the status line can advertise it on open and `d` can jump to it. No-op
// without a primary replay or a non-empty diff trace.
func (m Model) WithReplayDiff(r *trace.Replay) Model {
	if m.TraceReplay == nil || r == nil || r.Len() == 0 {
		return m
	}
	m.ReplayDiff = r
	m.Diverge = trace.Diff(m.TraceReplay, r)
	if m.Diverge.Found {
		m.Status = fmt.Sprintf("diff loaded — diverges at CYC:%d (frame %d). press d, D jumps here.",
			m.Diverge.Cycle, m.Diverge.Index+1)
	} else {
		m.Status = "diff loaded — traces identical over their overlap"
	}
	return m
}

// replayStatus is the standard "frame N/M" status line for trace replay.
func (m *Model) replayStatus() string {
	if m.TraceReplay == nil {
		return ""
	}
	return fmt.Sprintf("trace replay (frame %d/%d)",
		m.TraceReplay.Index+1, m.TraceReplay.Len())
}

// applyTraceFrame syncs the CPU's registers from the current Replay
// frame so every render path reads as if the live CPU were paused at
// that PC. No-op when TraceReplay is nil.
func (m *Model) applyTraceFrame() {
	if m.TraceReplay == nil {
		return
	}
	f, ok := m.TraceReplay.Current()
	if !ok {
		return
	}
	m.CPU.A, m.CPU.X, m.CPU.Y = f.A, f.X, f.Y
	m.CPU.SP, m.CPU.P, m.CPU.PC = f.SP, f.P, f.PC
	m.CPU.Cycles = f.Cycles
	m.CPU.Halted = false
}

// WithSourceMap loads PC->(file,line) mapping from cc65 .dbg file lines.
func (m Model) WithSourceMap(sm *symbols.SourceMap) Model {
	if sm == nil {
		return m
	}
	m.SrcMap = sm
	m.PCToSrc = sm.PCToSrc
	m.SourceFiles = sm.Files
	m.DataRanges = sm.DataRanges
	// Push the source map into the in-process DAP server so local-mode
	// stackTrace frames carry source lines (issue #449); refresh the cache.
	if ls, ok := m.Source.(*LocalSource); ok {
		ls.SetSymbols(nil, sm)
		m.syncStack()
	}
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.scheduleTick()}
	if c := waitForDAPEvent(m.Source); c != nil {
		cmds = append(cmds, c)
	}
	return tea.Batch(cmds...)
}

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
		// Quake console owns all input while open. Backtick toggles
		// from anywhere except the other modal states above (those
		// gates fire first so the console can't fight them).
		if m.ConsoleActive {
			return m.updateConsole(msg)
		}
		// Diff overlay: step keys scroll both columns (the view re-centres
		// on the primary cursor), D jumps to divergence, d/esc/q close.
		if m.ShowDiff {
			switch msg.String() {
			case "ctrl+c":
				m.saveState()
				return m, tea.Quit
			case "s", "right", "n":
				m.TraceReplay.Step(1)
				m.applyTraceFrame()
				m.Status = m.replayStatus()
				return m, m.scheduleTick()
			case "<", "left", "p":
				m.TraceReplay.Step(-1)
				m.applyTraceFrame()
				m.Status = m.replayStatus()
				return m, m.scheduleTick()
			case "D":
				if m.Diverge.Found {
					m.TraceReplay.Index = m.Diverge.Index
					m.applyTraceFrame()
					m.Status = fmt.Sprintf("jumped to divergence — CYC:%d (frame %d/%d)",
						m.Diverge.Cycle, m.Diverge.Index+1, m.TraceReplay.Len())
				}
				return m, m.scheduleTick()
			}
			// d / esc / q / any other key closes the overlay.
			m.ShowDiff = false
			m.Status = "diff view closed"
			return m, m.scheduleTick()
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
		if m.ShowSyms {
			return m.updateSymsManager(msg)
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
		case "`":
			m.ConsoleActive = true
			m.ConsoleBuf = ""
			m.Status = "console"
			// First-open onboarding hint — printed only when the
			// scrollback is empty so it doesn't spam on repeat
			// opens.
			if len(m.ConsoleScrollback) == 0 {
				m.appendConsole("chippy console — same verbs as `:` prompt. Tab completes.")
				m.appendConsole("Try: help, syms, bp <addr>, goto <addr|sym>, watch <addr>, speed N, theme NAME.")
				m.appendConsole("Esc or ` closes.")
			}
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
		case "d":
			if m.ReplayDiff == nil {
				m.Status = "diff: load a second trace with -diff"
				break
			}
			m.ShowDiff = !m.ShowDiff
			if m.ShowDiff {
				m.Status = "diff view (d closes, D jumps to divergence)"
			} else {
				m.Status = "diff view closed"
			}
		case "D":
			if !m.Diverge.Found {
				m.Status = "diff: no divergence to jump to"
				break
			}
			m.TraceReplay.Index = m.Diverge.Index
			if m.ReplayDiff != nil {
				m.ReplayDiff.Index = m.Diverge.Index
			}
			m.applyTraceFrame()
			m.Status = fmt.Sprintf("jumped to divergence — CYC:%d (frame %d/%d)",
				m.Diverge.Cycle, m.Diverge.Index+1, m.TraceReplay.Len())
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
			if m.TraceReplay != nil {
				m.TraceReplay.Step(1)
				m.applyTraceFrame()
				m.Status = m.replayStatus()
			} else if m.CPU.Halted {
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
			if m.Source != nil && m.Source.Attached() {
				if m.Running {
					if err := m.Source.Continue(); err != nil {
						m.Running = false
						m.Status = fmt.Sprintf("continue: %v", err)
						break
					}
				} else {
					if err := m.Source.Pause(); err != nil {
						m.Status = fmt.Sprintf("pause: %v", err)
					}
				}
			}
			if m.Running {
				m.Status = fmt.Sprintf("running @ %s", speedLabel(m.TargetHz))
			} else {
				m.Status = "paused"
			}
		case "R":
			if m.Source != nil && m.Source.Attached() {
				m.Status = "reset: not supported in attach mode"
				break
			}
			m.CPU.Reset()
			if m.Rewind != nil {
				m.Rewind.Reset()
			}
			m.Keyframes.Reset()
			m.StepCount = 0
			m.Status = "reset"
		case "<":
			if m.TraceReplay != nil {
				m.TraceReplay.Step(-1)
				m.applyTraceFrame()
				m.Status = m.replayStatus()
				break
			}
			if m.Rewind == nil || m.Rewind.Len() == 0 {
				m.Status = "rewind: empty"
				break
			}
			s, _ := m.Rewind.Pop()
			m.CPU.Restore(s, m.RAM)
			m.restoreperipherals(s)
			if m.StepCount > 0 {
				m.StepCount--
			}
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
			m.syncSourceBreakpoints()
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
			if m.ShowSource {
				m.sourceScroll(-1)
			} else {
				m.disasmScroll(-1)
			}
		case "]":
			if m.ShowSource {
				m.sourceScroll(+1)
			} else {
				m.disasmScroll(+1)
			}
		case "{":
			if m.ShowSource {
				m.sourceScroll(-8)
			} else {
				m.disasmScroll(-8)
			}
		case "}":
			if m.ShowSource {
				m.sourceScroll(+8)
			} else {
				m.disasmScroll(+8)
			}
		case "'":
			if m.ShowSource {
				m.SourceFollow = true
				m.Status = "source: follow PC"
			} else {
				m.DisasmFollow = true
				m.Status = "disasm: follow PC"
			}
		case "T":
			m.StackAnnotate = !m.StackAnnotate
			if m.StackAnnotate {
				m.Status = "stack: annotated"
			} else {
				m.Status = "stack: raw bytes"
			}
		}
		m.syncRegs()  // refresh the DAP-sourced Registers panel after key actions
		m.syncStack() // refresh the DAP-sourced Stack panel after key actions
		m.syncFlags() // refresh the DAP-sourced Flags panel after key actions
		return m, m.scheduleTick()

	case dapEventMsg:
		switch msg.ev.Event {
		case dap.ChippyStateEvent:
			// Server-pushed live state during a remote free-run: refresh the
			// Registers panel without a per-frame DAP request (issue #395).
			var cs dap.ChippyStateBody
			if err := remarshal(msg.ev.Body, &cs); err == nil {
				m.Regs = RegSnapshot{
					A: cs.A, X: cs.X, Y: cs.Y, SP: cs.SP, P: cs.P,
					PC: cs.PC, Cycles: cs.Cycles, Halted: cs.Halted,
				}
				m.Flags = flagsFromP(cs.P) // keep the Flags panel live during a remote run (#450)
				m.CPU.PC = cs.PC           // keep the mirror PC current for other panels
				// Apply streamed memory deltas (issue #440) so the memory and
				// disassembly panels stay live during a remote run without a
				// per-frame readMemory; the stopped event does a final
				// full-RAM reconcile. Start+len(Data) is authoritative.
				for _, r := range cs.DirtyRanges {
					if len(r.Data) > 0 {
						m.RAM.Load(r.Start, r.Data)
					}
				}
			}
		case "stopped":
			wasRunning := m.Running
			m.Running = false
			_ = m.Source.RefreshRegs()
			_ = m.Source.RefreshMemory()
			m.syncRegs()  // pull post-stop regs into the snapshot the panel renders
			m.syncStack() // and the post-stop stack frames (issue #449)
			m.syncFlags() // and the post-stop P-flag bits (issue #450)
			// Only overwrite Status when we were running — single
			// step paths have their own "stepped" / "hit bp"
			// messages and don't want this generic one stomping
			// theirs. The Running→false transition is what marks
			// "the server actually halted on its own".
			if wasRunning {
				m.Status = fmt.Sprintf("stopped at $%04X", m.CPU.PC)
			}
		case "terminated":
			return m, tea.Quit
		}
		return m, waitForDAPEvent(m.Source)
	case dapClosedMsg:
		m.Status = "remote disconnected"
		m.Running = false
		return m, tea.Quit
	case tickMsg:
		if m.Running {
			// In remote-attach mode the server owns execution after a
			// `continue` request; the local tickMsg loop must not
			// step. The server's `stopped` event flips m.Running back
			// off via the dap event pump.
			if m.Source != nil && m.Source.Attached() {
				return m, m.scheduleTick()
			}
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
		m.syncRegs()  // refresh the DAP-sourced Registers panel once per tick
		m.syncStack() // refresh the DAP-sourced Stack panel once per tick
		m.syncFlags() // refresh the DAP-sourced Flags panel once per tick
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
//
// CPUMu (if set by `:dap`) serializes this step with a concurrent DAP
// server. The mutex is held for the snapshot + step + delta-claim
// triple so a DAP handler can't see a partially-rewound state.
func (m *Model) step() int {
	if m.CPUMu != nil {
		m.CPUMu.Lock()
		defer m.CPUMu.Unlock()
	}
	if m.Rewind == nil {
		n := m.Source.Step()
		m.StepCount++
		return n
	}
	m.seedKeyframe()
	snap := m.CPU.Snapshot(m.RAM)
	m.captureperipherals(&snap)
	m.RAM.ResetShadow()
	n := m.Source.Step()
	snap.Pages = m.RAM.TakeShadow()
	m.Rewind.Push(snap)
	m.StepCount++
	m.maybeKeyframe()
	return n
}

// seedKeyframe captures the step-0 keyframe — the machine state before the
// very first step — so deep rewinds to any target below the first interval
// boundary have a base to replay forward from. Runs once per run (guarded by
// StepCount==0) and never during replay.
func (m *Model) seedKeyframe() {
	if m.Keyframes == nil || m.replayingRewind || m.StepCount != 0 || m.Keyframes.Len() > 0 {
		return
	}
	kf := cpu.Keyframe{Step: 0, Snap: m.CPU.SnapshotFull(m.RAM)}
	m.captureperipherals(&kf.Snap)
	m.Keyframes.Push(kf)
}

// maybeKeyframe captures a full-RAM keyframe at every keyframeInterval-th
// step so `:rewind` can reach far past the fine ring. Skipped while replaying
// (the keyframes for that span already exist) and when deep rewind is off.
func (m *Model) maybeKeyframe() {
	if m.Keyframes == nil || m.replayingRewind {
		return
	}
	if m.StepCount%keyframeInterval != 0 {
		return
	}
	kf := cpu.Keyframe{Step: m.StepCount, Snap: m.CPU.SnapshotFull(m.RAM)}
	m.captureperipherals(&kf.Snap)
	m.Keyframes.Push(kf)
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

// syncSourceBreakpoints pushes the current PC breakpoint set to the
// Source. LocalSource is a no-op (the Model's run-loop checks the
// `m.Breakpoints` map inline); RemoteSource forwards via DAP
// `setInstructionBreakpoints`. Called after any mutation of the
// breakpoint map.
func (m *Model) syncSourceBreakpoints() {
	if m.Source == nil {
		return
	}
	bps := make([]SourceBP, 0, len(m.Breakpoints))
	for pc, bp := range m.Breakpoints {
		if bp == nil || !bp.Enabled || bp.Rejected {
			continue
		}
		bps = append(bps, SourceBP{
			PC:       pc,
			Cond:     bp.Cond,
			HitLimit: bp.HitLimit,
			Log:      bp.Log,
		})
	}
	_ = m.Source.SetBreakpoints(bps)
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
	case "c":
		// Edit / clear the conditional expression on the selected bp.
		// Opens the prompt pre-populated with `:bp $XXXX if <existing>`;
		// user finishes the line + enter. To clear, leave nothing after
		// `if`.
		if m.BPCursor < len(bps) {
			addr := bps[m.BPCursor]
			if bp := m.Breakpoints[addr]; bp != nil {
				m.ShowBPs = false
				m.PromptActive = true
				m.PromptBuf = fmt.Sprintf("bp $%04X if %s", addr, bp.Cond)
				m.Status = "edit cond — enter to commit"
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
		watchH = m.watchRowCount() + 3
		if watchH > 12 {
			watchH = 12
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
		if m.Keyframes.Len() > 0 {
			rewindSeg += fmt.Sprintf(" deep:%s@%dMiB", humanCount(m.rewindReachSteps()), m.RewindBudgetMB)
		}
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
	case m.ShowDiff:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.diffModal(bodyHeight))
	case m.ShowHelp:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.helpModal())
	case m.ShowBPs:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.bpModal())
	case m.ShowSyms:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.symsModal())
	case m.ImmediateActive:
		bodyBlock = lipgloss.Place(m.W, bodyHeight, lipgloss.Center, lipgloss.Center, m.immediateModal())
	case m.ConsoleActive:
		// Quake drop-down: console anchored at the top, the rest of
		// the body still visible behind it (only top half consumed).
		console := m.consoleView(m.W, bodyHeight)
		bodyBlock = lipgloss.JoinVertical(lipgloss.Left,
			console,
			lipgloss.PlaceHorizontal(m.W, lipgloss.Center, body),
		)
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
				{":rewind N", "rewind N steps (keyframe replay for deep jumps)"},
				{":rewind-budget MB", "cap keyframe memory; sets deep-rewind reach"},
				{"r", "run / pause"},
				{"R", "reset CPU"},
				{"b", "toggle breakpoint at PC"},
				{"B", "breakpoint manager"},
				{":syms", "browse labels from the loaded .dbg (enter toggles bp)"},
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
				{":watch X [byte|word] [xN] [label]", "add value watch (xN expands an array; also :watch reg <name>)"},
				{":watch X as {f:byte,g:word}", "struct overlay: named member rows at X+offset (#409)"},
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
				{":mem $ADDR V [V...]", "write hex bytes via the bus (MMIO + watches fire)"},
			}},
			{"Disassembly / Source", [][2]string{
				{"[ / ]", "scroll one line/instruction up/down (active panel)"},
				{"{ / }", "scroll eight lines/instructions up/down"},
				{"'", "re-follow PC (drops pinned-anchor mode)"},
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
			{"DAP attach", [][2]string{
				{":dap", "show current listener (or `no listener`)"},
				{":dap PORT", "start a TCP DAP listener so editors can attach"},
				{":dap 0", "auto-assign a free port"},
				{":dap stop", "close the listener"},
			}},
			{"Trace replay", [][2]string{
				{"--trace-replay PATH", "open a recorded trace; step keys scroll frames"},
				{"s / <", "advance / rewind one trace frame (CPU stays paused)"},
				{":find EXPR", "jump to next frame matching expr (:rfind = backward)"},
				{":cycle N", "jump to first frame at/after cycle N (binary search)"},
				{"--diff PATH", "load a 2nd trace; mark first divergence cycle"},
				{"d / D", "toggle diff side-by-side view / jump to divergence"},
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
	b.WriteString(help.Render("  j/k move  d delete  e enable/disable  c edit cond  enter set PC  esc close"))
	return lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("196")).
		Padding(1, 3).
		Render(b.String())
}

// ---------- panels ----------

// regsView renders the Registers panel from m.Regs — a DAP-sourced snapshot
// (issue #394), never direct cpu.CPU field access. m.syncRegs() refreshes the
// snapshot in the Update loop.
func (m Model) regsView(w, h int) string {
	r := m.Regs
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	row := func(label string, hex string) string {
		return regStyle.Render(label) + " " + val.Render(hex)
	}
	state := lipgloss.NewStyle().Foreground(lipgloss.Color("46")).Bold(true).Render("RUN ")
	if r.Halted {
		state = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true).Render("HALT")
	} else if m.Running {
		state = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render("RUN*")
	}
	body := fmt.Sprintf("%s   %s   %s\n%s   %s   %s",
		row("A: ", fmt.Sprintf("$%02X", r.A)),
		row("X: ", fmt.Sprintf("$%02X", r.X)),
		row("Y: ", fmt.Sprintf("$%02X", r.Y)),
		row("SP:", fmt.Sprintf("$%02X", r.SP)),
		row("PC:", fmt.Sprintf("$%04X", r.PC)),
		state,
	)
	return fitPanel("Registers", body, w, h)
}

// flagsView renders the Flags panel from m.Flags — a DAP-sourced snapshot of
// the decomposed P bits (issue #450), never direct cpu.CPU.P access.
// m.syncFlags() refreshes the cache in the Update loop, so View stays pure.
func (m Model) flagsView(w, h int) string {
	f := m.Flags
	flag := func(name string, on bool) string {
		if on {
			return flagOn.Render(name)
		}
		return flagOff.Render(strings.ToLower(name))
	}
	body := fmt.Sprintf("%s %s %s %s  %s %s %s %s",
		flag("N", f.N),
		flag("V", f.V),
		flag("U", f.U),
		flag("B", f.B),
		flag("D", f.D),
		flag("I", f.I),
		flag("Z", f.Z),
		flag("C", f.C),
	)
	return fitPanel("Flags", body, w, h)
}

// maxWatchElemRows caps how many array elements a single array watch
// renders in the panel; the rest collapse into a "… +N more" line.
const maxWatchElemRows = 8

// watchRowCount is the number of display rows the watch panel needs: one
// per scalar/register watch, and 1 header + min(Count, cap)+1 rows per
// array watch. Used to size the panel before rendering.
func (m Model) watchRowCount() int {
	n := 0
	for _, wt := range m.Watches {
		if wt.Kind == "mem" && len(wt.Fields) > 0 {
			n += 1 + len(wt.Fields) // header + one row per member
			continue
		}
		if wt.Kind == "mem" && wt.Count > 1 {
			shown := wt.Count
			if shown > maxWatchElemRows {
				shown = maxWatchElemRows + 1 // elements + "… +N more"
			}
			n += 1 + shown
			continue
		}
		n++
	}
	return n
}

func (m Model) watchView(w, h int) string {
	nameStyle := dimAddr
	valStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Bold(true)
	var b strings.Builder
	for _, wt := range m.Watches {
		if wt.Kind == "mem" && len(wt.Fields) > 0 {
			m.writeStructWatch(&b, wt, nameStyle, valStyle)
			continue
		}
		if wt.Kind == "mem" && wt.Count > 1 {
			m.writeArrayWatch(&b, wt, nameStyle, valStyle)
			continue
		}
		var val, name string
		if wt.Kind == "reg" {
			val = m.regValue(wt.Reg)
			name = wt.Label
			if name == "" {
				name = wt.Reg
			}
		} else {
			val = m.fmtMemValue(wt.Addr, wt.Width)
			name = wt.Label
			if name == "" {
				name = fmt.Sprintf("$%04X", wt.Addr)
			}
		}
		fmt.Fprintf(&b, "%s %s\n",
			nameStyle.Render(fmt.Sprintf("%-10s", name)),
			valStyle.Render(val))
	}
	return fitPanel("Watch", strings.TrimRight(b.String(), "\n"), w, h)
}

// fmtMemValue reads one watch element (byte or LE word) and formats it.
func (m Model) fmtMemValue(addr uint16, width int) string {
	if width == 2 {
		lo := uint16(m.RAM.Read(addr))
		hi := uint16(m.RAM.Read(addr + 1))
		return fmt.Sprintf("$%04X", lo|(hi<<8))
	}
	return fmt.Sprintf("  $%02X", m.RAM.Read(addr))
}

// writeArrayWatch renders an array watch as a header row plus one indented
// row per element (name[i]), capped at maxWatchElemRows with a trailing
// "… +N more" when truncated.
func (m Model) writeArrayWatch(b *strings.Builder, wt Watch, nameStyle, valStyle lipgloss.Style) {
	name := wt.Label
	if name == "" {
		name = fmt.Sprintf("$%04X", wt.Addr)
	}
	fmt.Fprintf(b, "%s %s\n",
		nameStyle.Render(fmt.Sprintf("%-10s", name)),
		valStyle.Faint(true).Render(fmt.Sprintf("[%d]", wt.Count)))
	shown := wt.Count
	if shown > maxWatchElemRows {
		shown = maxWatchElemRows
	}
	for i := 0; i < shown; i++ {
		addr := wt.Addr + uint16(i*wt.Width)
		fmt.Fprintf(b, "  %s %s\n",
			nameStyle.Render(fmt.Sprintf("%-8s", fmt.Sprintf("[%d]", i))),
			valStyle.Render(m.fmtMemValue(addr, wt.Width)))
	}
	if wt.Count > shown {
		fmt.Fprintf(b, "  %s\n", nameStyle.Render(fmt.Sprintf("… +%d more", wt.Count-shown)))
	}
}

// writeStructWatch renders a struct-overlay watch (issue #409) as a header
// row plus one indented row per declared member, read at Addr+Offset.
func (m Model) writeStructWatch(b *strings.Builder, wt Watch, nameStyle, valStyle lipgloss.Style) {
	name := wt.Label
	if name == "" {
		name = fmt.Sprintf("$%04X", wt.Addr)
	}
	fmt.Fprintf(b, "%s %s\n",
		nameStyle.Render(fmt.Sprintf("%-10s", name)),
		valStyle.Faint(true).Render(fmt.Sprintf("{%d}", len(wt.Fields))))
	for _, f := range wt.Fields {
		addr := wt.Addr + uint16(f.Offset)
		fmt.Fprintf(b, "  %s %s\n",
			nameStyle.Render(fmt.Sprintf("%-8s", f.Name)),
			valStyle.Render(m.fmtMemValue(addr, f.Width)))
	}
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

// sourceScroll moves the source anchor by `delta` lines (sign matters).
// Switches the panel into pinned mode so it stops following PC. On the
// first scroll we bootstrap the anchor from the current PC's source
// location so the user starts from where they were looking.
func (m *Model) sourceScroll(delta int) {
	if m.SourceFollow {
		loc, ok := m.PCToSrc[m.CPU.PC]
		if !ok {
			if near, nok := m.nearestSrcLocBelow(m.CPU.PC); nok {
				loc = near
				ok = true
			}
		}
		if !ok {
			m.Status = "source: no mapping for current PC"
			return
		}
		m.SourceAnchorFile = loc.File
		m.SourceAnchorLine = loc.Line
		m.SourceFollow = false
	}
	lines, ok := m.SourceFiles[m.SourceAnchorFile]
	if !ok || len(lines) == 0 {
		m.Status = fmt.Sprintf("source: %s unavailable", m.SourceAnchorFile)
		return
	}
	n := m.SourceAnchorLine + delta
	if n < 1 {
		n = 1
	}
	if n > len(lines) {
		n = len(lines)
	}
	m.SourceAnchorLine = n
	m.Status = fmt.Sprintf("source @ %s:%d (' to follow PC)", m.SourceAnchorFile, n)
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

	// pcLoc: where the CPU's PC maps in source (may differ from the
	// centered file/line when the user has scrolled away). Used to draw
	// the 👉 marker even in pinned mode.
	pcLoc, pcOk := m.PCToSrc[m.CPU.PC]
	// Fallback: cc65 runtime stubs (pusha, copydata, etc.) have no
	// source mapping in the .dbg. Rather than blanking the panel mid-
	// step, find the nearest mapped PC at or below the current one and
	// show its source — with a hint that we're inside generated code.
	inGenerated := false
	if !pcOk {
		if near, nok := m.nearestSrcLocBelow(m.CPU.PC); nok {
			pcLoc = near
			pcOk = true
			inGenerated = true
		}
	}

	// Pick what to center on. Follow mode = wherever PC is now. Pinned
	// mode = the user's anchor.
	var centerFile string
	var centerLine int
	if m.SourceFollow {
		if !pcOk {
			return fitPanel("Source", help.Render("  (no source mapping for current PC)"), w, h)
		}
		centerFile = pcLoc.File
		centerLine = pcLoc.Line
	} else {
		centerFile = m.SourceAnchorFile
		centerLine = m.SourceAnchorLine
	}

	lines, lok := m.SourceFiles[centerFile]
	if !lok || len(lines) == 0 {
		return fitPanel("Source", help.Render(fmt.Sprintf("  (file unavailable: %s)", centerFile)), w, h)
	}

	if centerLine < 1 {
		centerLine = 1
	}
	if centerLine > len(lines) {
		centerLine = len(lines)
	}
	cur := centerLine - 1 // 0-indexed
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
		if l, ok := m.PCToSrc[pc]; ok && l.File == centerFile {
			if _, dup := bpLines[l.Line]; !dup {
				bpLines[l.Line] = bp
			}
		}
	}

	pcLineInView := pcOk && pcLoc.File == centerFile

	var b strings.Builder
	var title string
	switch {
	case !m.SourceFollow:
		title = fmt.Sprintf("Source — %s:%d  [pinned · ' follows PC]", centerFile, centerLine)
	case inGenerated:
		title = fmt.Sprintf("Source — %s:%d  (PC $%04X in generated code)", centerFile, centerLine, m.CPU.PC)
	default:
		title = fmt.Sprintf("Source — %s:%d", centerFile, centerLine)
	}
	for i := start; i < end; i++ {
		lineNum := i + 1
		var marker string
		switch {
		case pcLineInView && lineNum == pcLoc.Line:
			marker = "\U0001F449"
		default:
			if bp, ok := bpLines[lineNum]; ok {
				marker = bp.marker()
			} else {
				marker = "  "
			}
		}
		text := fmt.Sprintf("%s %s  %s", marker, dimAddr.Render(fmt.Sprintf("%4d", lineNum)), lines[i])
		// Highlight: in follow mode highlight the PC line (which is the
		// center). In pinned mode highlight the cursor / anchor line so
		// the user can see where they've scrolled to.
		switch {
		case m.SourceFollow && pcLineInView && lineNum == pcLoc.Line:
			text = curLine.Render(text)
		case !m.SourceFollow && lineNum == centerLine:
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
