// Package tui — memory access watchpoint type and helpers.
//
// Mirrors the rich exec Breakpoint (see bp.go): enable, hit count, condition,
// log point. Tracks reads, writes, or both at a single address.
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nkane/chippy/cpu"
)

// MemBPKind is one of "r", "w", "rw".
type MemBPKind string

const (
	MemBPRead      MemBPKind = "r"
	MemBPWrite     MemBPKind = "w"
	MemBPReadWrite MemBPKind = "rw"
)

// MemBP describes a memory access watchpoint.
type MemBP struct {
	Addr     uint16    `json:"addr"`
	Kind     MemBPKind `json:"kind"`
	Enabled  bool      `json:"enabled"`
	Hits     int       `json:"hits,omitempty"`
	HitLimit int       `json:"hitLimit,omitempty"` // 0 unlimited; >0 Nth; -1 one-shot
	Cond     string    `json:"cond,omitempty"`
	Log      string    `json:"log,omitempty"`

	// Compiled condition. Not persisted.
	condFn func(*cpu.CPU, cpu.Bus) bool `json:"-"`
}

// newMemBP returns a plain enabled watchpoint.
func newMemBP(addr uint16, kind MemBPKind) *MemBP {
	return &MemBP{Addr: addr, Kind: kind, Enabled: true}
}

// matches returns true if this watchpoint cares about the given access kind
// ("r" or "w"). RW watches accept either.
func (b *MemBP) matches(access MemBPKind) bool {
	if b == nil || !b.Enabled {
		return false
	}
	if b.Kind == MemBPReadWrite {
		return true
	}
	return b.Kind == access
}

// marker returns the gutter glyph for this watchpoint kind.
//
//	👁 read   ✏ write   🔁 read+write
func (b *MemBP) marker() string {
	if b == nil {
		return "  "
	}
	switch b.Kind {
	case MemBPRead:
		return "\U0001F441" // 👁
	case MemBPWrite:
		return "\u270F\uFE0F" // ✏
	case MemBPReadWrite:
		return "\U0001F501" // 🔁
	}
	return "??"
}

// kindLabel returns a short text label ("R", "W", "RW") for modal display.
func (b *MemBP) kindLabel() string {
	switch b.Kind {
	case MemBPRead:
		return "R"
	case MemBPWrite:
		return "W"
	case MemBPReadWrite:
		return "RW"
	}
	return "?"
}

// describe formats one line for the BP manager modal.
func (b *MemBP) describe() string {
	out := fmt.Sprintf("%s $%04X [%s]", b.marker(), b.Addr, b.kindLabel())
	if !b.Enabled {
		out += "  [disabled]"
	}
	if b.HitLimit > 0 {
		out += fmt.Sprintf("  [%d/%d]", b.Hits, b.HitLimit)
	} else if b.HitLimit == -1 {
		out += "  [once]"
	} else if b.Hits > 0 {
		out += fmt.Sprintf("  [%d hits]", b.Hits)
	}
	if b.Cond != "" {
		out += "  if " + b.Cond
	}
	if b.Log != "" {
		out += "  log " + b.Log
	}
	return out
}

// cmdMemBP handles `:bpr`, `:bpw`, `:bprw`. Same modifier syntax as :bp:
//
//	:bpw $0200                       toggle write watch at $0200
//	:bpw $0200 once                  one-shot
//	:bpw $0200 hits 5                fire on 5th write
//	:bpw $0200 if A==$FF             conditional
//	:bpr $FFFC log read PC={PC}      log point on read
func (m *Model) cmdMemBP(args []string, kind MemBPKind) string {
	if len(args) == 0 {
		return fmt.Sprintf("usage: :bp%s ADDR [once|hits N|if EXPR|log MSG]", string(kind))
	}
	addr, err := m.parseAddrSym(args[0])
	if err != nil {
		return err.Error()
	}
	rest := args[1:]

	// No modifiers -> toggle.
	if len(rest) == 0 {
		if existing, ok := m.MemBPs[addr]; ok && existing.Kind == kind {
			delete(m.MemBPs, addr)
			m.saveState()
			return fmt.Sprintf("mem bp -%s$%04X", string(kind), addr)
		}
		bp := newMemBP(addr, kind)
		m.MemBPs[addr] = bp
		m.saveState()
		return fmt.Sprintf("%s mem bp +%s$%04X", bp.marker(), string(kind), addr)
	}

	// With modifiers -> always (re)create.
	bp := newMemBP(addr, kind)
	if errStr := parseMemBPModifiers(bp, rest); errStr != "" {
		return errStr
	}
	if bp.Cond != "" {
		fn, cerr := compileCondition(bp.Cond, m.Syms)
		if cerr != nil {
			return "bad cond: " + cerr.Error()
		}
		bp.condFn = fn
	}
	m.MemBPs[addr] = bp
	m.saveState()
	return fmt.Sprintf("%s mem bp +%s$%04X", bp.marker(), string(kind), addr)
}

// parseMemBPModifiers mirrors parseBPModifiers but writes to a MemBP.
func parseMemBPModifiers(bp *MemBP, args []string) string {
	for i := 0; i < len(args); i++ {
		switch strings.ToLower(args[i]) {
		case "once":
			bp.HitLimit = -1
		case "hits":
			if i+1 >= len(args) {
				return "usage: ... hits N"
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n <= 0 {
				return fmt.Sprintf("bad hit count: %s", args[i+1])
			}
			bp.HitLimit = n
			i++
		case "if":
			bp.Cond = strings.Join(args[i+1:], " ")
			return ""
		case "log":
			bp.Log = strings.Join(args[i+1:], " ")
			return ""
		default:
			return fmt.Sprintf("unknown modifier: %s", args[i])
		}
	}
	return ""
}

// processMemHits drains pending memory hits from the WBus and applies
// each watch's condition / hit-count / log-point logic. Returns (pause,
// status). If pause is true, caller should stop the run loop. status is a
// log-point message to display (last one wins).
//
// One-shot watches (HitLimit == -1) are deleted from m.MemBPs after firing.
func (m *Model) processMemHits() (bool, string) {
	if m.WBus == nil {
		return false, ""
	}
	hits := m.WBus.Drain()
	if len(hits) == 0 {
		return false, ""
	}
	pause := false
	var status string
	for _, h := range hits {
		bp := m.MemBPs[h.Addr]
		if bp == nil || !bp.matches(h.Kind) {
			continue
		}
		if bp.condFn != nil && !bp.condFn(m.CPU, m.WBus.Inner) {
			continue
		}
		bp.Hits++
		if bp.HitLimit > 0 && bp.Hits < bp.HitLimit {
			continue
		}
		if bp.Log != "" {
			status = formatLog(bp.Log, m.CPU, m.WBus.Inner)
			continue
		}
		if bp.HitLimit == -1 {
			delete(m.MemBPs, h.Addr)
		}
		pause = true
		status = fmt.Sprintf("hit %s $%04X (%s=$%02X) PC=$%04X",
			bp.kindLabel(), h.Addr, h.Kind, h.Value, h.PC)
	}
	return pause, status
}
