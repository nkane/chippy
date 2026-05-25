// Package tui — breakpoint type and helpers.
//
// The Breakpoints map went from map[uint16]bool to map[uint16]*Breakpoint to
// support enable/disable, hit counts, conditions, log points, and origin
// tracking (e.g. resolved-from-source-line).
package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nkane/chippy/cpu"
)

// Breakpoint describes a single execution breakpoint. Memory watchpoints have
// their own type (see wbus.go when implemented).
type Breakpoint struct {
	Addr     uint16 `json:"addr"`
	Enabled  bool   `json:"enabled"`
	Hits     int    `json:"hits,omitempty"`
	HitLimit int    `json:"hitLimit,omitempty"` // 0 = unlimited; >0 = break on/after Nth hit; -1 = one-shot (auto-delete)
	Cond     string `json:"cond,omitempty"`     // raw expression, "" = unconditional
	Source   string `json:"source,omitempty"`   // optional "file:line" tag from :bp file:line
	Log      string `json:"log,omitempty"`      // log point template; non-empty makes this a logpoint (don't pause)
	Rejected bool   `json:"rejected,omitempty"` // resolution failed; show 💩 marker

	// Compiled condition. Not persisted; rebuilt from Cond on load.
	condFn func(*cpu.CPU, cpu.Bus) bool `json:"-"`
}

// newBP returns a sensible plain enabled breakpoint at addr.
func newBP(addr uint16) *Breakpoint {
	return &Breakpoint{Addr: addr, Enabled: true}
}

// kind returns the marker glyph for this breakpoint, matching DAP sigils:
//
//	🛑 plain   🔶 conditional   📜 logpoint   💩 rejected
func (b *Breakpoint) marker() string {
	if b == nil {
		return "  "
	}
	switch {
	case b.Rejected:
		return "\U0001F4A9" // 💩
	case b.Log != "":
		return "\U0001F4DC" // 📜
	case b.Cond != "":
		return "\U0001F536" // 🔶
	default:
		return "\U0001F6D1" // 🛑
	}
}

// shouldBreakAt returns (pause, logMessage). pause=true means halt the run
// loop; logMessage non-empty means emit it (used by log points). Increments
// the hit counter and may auto-delete one-shot bps.
func (m *Model) shouldBreakAt(pc uint16) (bool, string) {
	bp, ok := m.Breakpoints[pc]
	if !ok || bp == nil || !bp.Enabled || bp.Rejected {
		return false, ""
	}
	// Evaluate condition first; a failing cond doesn't count as a hit so users
	// can iterate on the expression without state drift.
	if bp.condFn != nil {
		if !bp.condFn(m.CPU, m.RAM) {
			return false, ""
		}
	}
	bp.Hits++
	// Hit-count gating.
	if bp.HitLimit > 0 && bp.Hits < bp.HitLimit {
		return false, ""
	}
	// Log point: format message, do not pause.
	if bp.Log != "" {
		return false, formatLog(bp.Log, m.CPU, m.RAM)
	}
	// One-shot: delete after first qualifying hit.
	if bp.HitLimit == -1 {
		delete(m.Breakpoints, pc)
	}
	return true, ""
}

// formatLog expands {A} {X} {Y} {P} {SP} {PC} and {[$XXXX]} substitutions in
// a log-point template against current CPU/bus state.
func formatLog(tmpl string, c *cpu.CPU, bus cpu.Bus) string {
	var out strings.Builder
	i := 0
	for i < len(tmpl) {
		if tmpl[i] != '{' {
			out.WriteByte(tmpl[i])
			i++
			continue
		}
		end := strings.IndexByte(tmpl[i:], '}')
		if end < 0 {
			out.WriteString(tmpl[i:])
			break
		}
		token := tmpl[i+1 : i+end]
		out.WriteString(evalLogToken(token, c, bus))
		i += end + 1
	}
	return out.String()
}

func evalLogToken(tok string, c *cpu.CPU, bus cpu.Bus) string {
	tok = strings.TrimSpace(tok)
	switch strings.ToUpper(tok) {
	case "A":
		return fmt.Sprintf("$%02X", c.A)
	case "X":
		return fmt.Sprintf("$%02X", c.X)
	case "Y":
		return fmt.Sprintf("$%02X", c.Y)
	case "P":
		return fmt.Sprintf("$%02X", c.P)
	case "SP", "S":
		return fmt.Sprintf("$%02X", c.SP)
	case "PC":
		return fmt.Sprintf("$%04X", c.PC)
	}
	// Memory deref: [$XX] or [$XXXX].
	if strings.HasPrefix(tok, "[") && strings.HasSuffix(tok, "]") {
		inner := strings.TrimSpace(tok[1 : len(tok)-1])
		var addr uint32
		var err error
		switch {
		case strings.HasPrefix(inner, "$"):
			addr, err = parseHex(inner[1:])
		case strings.HasPrefix(inner, "0x"), strings.HasPrefix(inner, "0X"):
			addr, err = parseHex(inner[2:])
		default:
			return "{" + tok + "}"
		}
		if err != nil || addr > 0xFFFF {
			return "{" + tok + "}"
		}
		return fmt.Sprintf("$%02X", bus.Read(uint16(addr)))
	}
	return "{" + tok + "}"
}

func parseHex(s string) (uint32, error) {
	var v uint32
	for _, r := range s {
		var d uint32
		switch {
		case r >= '0' && r <= '9':
			d = uint32(r - '0')
		case r >= 'a' && r <= 'f':
			d = uint32(r-'a') + 10
		case r >= 'A' && r <= 'F':
			d = uint32(r-'A') + 10
		default:
			return 0, fmt.Errorf("bad hex %q", s)
		}
		v = v<<4 | d
	}
	return v, nil
}

// cmdBP handles the `:bp` command. Supported forms:
//
//	:bp $XXXX                    toggle plain bp at address (or symbol)
//	:bp file.s:42                resolve source line -> PC, set bp (or 💩 reject)
//	:bp $XXXX once               one-shot bp (deletes itself on hit)
//	:bp $XXXX hits N             break only on the Nth hit
//	:bp $XXXX if <expr>          conditional bp (🔶)
//	:bp $XXXX log <message>      log point (📜); never pauses, message templated
//
// Modifiers can be combined: `:bp main:42 if A==$FF hits 3 log A={A} X={X}`.
func (m *Model) cmdBP(args []string) string {
	target := args[0]
	rest := args[1:]

	// 1. Resolve target: either source-line "file:line" form, or addr/symbol.
	addr, src, err := m.resolveBPTarget(target)
	if err != nil {
		// Source-line miss creates a 💩 rejected bp so the user sees the
		// failure in the BP manager rather than just a status flash.
		if src != "" {
			pseudo := newBP(0)
			pseudo.Source = src
			pseudo.Rejected = true
			pseudo.Enabled = false
			// We have no addr to key on; stash it under a synthetic high addr
			// derived from the hash of src so collisions are unlikely.
			key := uint16(0xF000) | (hash16(src) & 0x0FFF)
			m.Breakpoints[key] = pseudo
			m.saveState()
			return "💩 " + err.Error()
		}
		return err.Error()
	}

	// 2. Toggle semantics only apply when no modifiers are present.
	if len(rest) == 0 {
		if _, ok := m.Breakpoints[addr]; ok {
			delete(m.Breakpoints, addr)
			m.saveState()
			m.syncSourceBreakpoints()
			return fmt.Sprintf("bp -$%04X", addr)
		}
		bp := newBP(addr)
		bp.Source = src
		m.Breakpoints[addr] = bp
		m.saveState()
		m.syncSourceBreakpoints()
		return fmt.Sprintf("bp +$%04X", addr)
	}

	// 3. With modifiers, always (re)create the bp.
	bp := newBP(addr)
	bp.Source = src
	if errStr := parseBPModifiers(bp, rest); errStr != "" {
		return errStr
	}
	// Compile condition if present.
	if bp.Cond != "" {
		fn, cerr := compileCondition(bp.Cond, m.Syms)
		if cerr != nil {
			return "bad cond: " + cerr.Error()
		}
		bp.condFn = fn
	}
	m.Breakpoints[addr] = bp
	m.saveState()
	m.syncSourceBreakpoints()
	return fmt.Sprintf("%s bp +$%04X", bp.marker(), addr)
}

// resolveBPTarget returns (addr, sourceTag, err). sourceTag is "file:line"
// when the user used that form (so we can flag rejects). Numeric/symbol
// targets return ("", nil err).
func (m *Model) resolveBPTarget(target string) (uint16, string, error) {
	// Source-line form: "name.ext:42". We require the colon to be after a
	// '.' to avoid colliding with hex like "$FF:00" (which we don't parse
	// anyway; this is just defensive).
	if i := strings.LastIndexByte(target, ':'); i > 0 {
		left := target[:i]
		right := target[i+1:]
		if strings.ContainsRune(left, '.') {
			lineNum, err := strconv.Atoi(right)
			if err == nil && lineNum > 0 {
				addr, ok := m.lookupSrcLine(left, lineNum)
				if !ok {
					return 0, target, fmt.Errorf("no PC for %s:%d", left, lineNum)
				}
				return addr, target, nil
			}
		}
	}
	addr, err := m.parseAddrSym(target)
	if err != nil {
		return 0, "", err
	}
	return addr, "", nil
}

// lookupSrcLine reverse-maps (file, line) -> first PC. Builds the reverse
// map lazily on first call (and rebuilds when PCToSrc grows; cheap since
// PCToSrc is itself loaded once).
func (m *Model) lookupSrcLine(file string, line int) (uint16, bool) {
	for pc, loc := range m.PCToSrc {
		if loc.Line == line && (loc.File == file || strings.HasSuffix(loc.File, "/"+file)) {
			return pc, true
		}
	}
	return 0, false
}

// parseBPModifiers consumes tokens like `once`, `hits N`, `if <expr...>`,
// `log <msg...>`. The `if` and `log` clauses are greedy: everything after
// the keyword belongs to that clause. So put them last in a single command.
func parseBPModifiers(bp *Breakpoint, args []string) string {
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
			return "" // greedy, done
		case "log":
			bp.Log = strings.Join(args[i+1:], " ")
			return "" // greedy, done
		default:
			return fmt.Sprintf("unknown modifier: %s", args[i])
		}
	}
	return ""
}

// hash16 produces a deterministic 16-bit fingerprint of s. Used to key
// rejected breakpoints in the Breakpoints map without colliding with real
// addresses (we OR with 0xF000).
func hash16(s string) uint16 {
	var h uint32 = 2166136261
	for _, b := range []byte(s) {
		h ^= uint32(b)
		h *= 16777619
	}
	return uint16(h ^ (h >> 16))
}
