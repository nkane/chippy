package tui

import (
	"fmt"
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
func (m Model) updatePrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.PromptActive = false
		m.PromptBuf = ""
		m.Status = "cancelled"
		return m, m.scheduleTick()
	case "enter":
		out := m.runCommand(strings.TrimSpace(m.PromptBuf))
		m.PromptActive = false
		m.PromptBuf = ""
		m.Status = out
		return m, m.scheduleTick()
	case "backspace":
		if len(m.PromptBuf) > 0 {
			m.PromptBuf = m.PromptBuf[:len(m.PromptBuf)-1]
		}
		return m, m.scheduleTick()
	}
	// Accept printable runes (single chars only — Bubble Tea reports them as KeyRunes).
	if len(msg.Runes) > 0 {
		m.PromptBuf += string(msg.Runes)
	}
	return m, m.scheduleTick()
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
		return fmt.Sprintf("mem -> $%04X", m.MemViewAddr)
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
		m.Running = true
		return fmt.Sprintf("running to $%04X", addr)
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
		width := 1
		label := ""
		if len(args) >= 2 {
			switch args[1] {
			case "word", "w", "16":
				width = 2
			case "byte", "b", "8":
				width = 1
			default:
				label = strings.Join(args[1:], " ")
			}
		}
		if label == "" && len(args) >= 3 {
			label = strings.Join(args[2:], " ")
		}
		if label == "" && m.Syms != nil {
			label = m.Syms.Lookup(addr)
		}
		m.Watches = append(m.Watches, Watch{Kind: "mem", Addr: addr, Label: label, Width: width})
		m.saveState()
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
	case "help", "?":
		m.ShowHelp = true
		return "help"
	case "q", "quit":
		return "use q outside prompt to quit"
	}
	return fmt.Sprintf("unknown: %s", cmd)
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
func (m Model) promptLine(width int) string {
	cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
	text := ":" + m.PromptBuf + cursor
	return promptStyle.Width(width).Render(text)
}
