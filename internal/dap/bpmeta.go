package dap

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nkane/chippy/internal/expr"
)

// bpMeta carries the condition / hit / log modifiers attached to a single
// breakpoint. nil meta means "plain bp — stop unconditionally."
//
// Hit count is stored on the meta itself, not on the Server, so resetting
// bps on a re-send naturally resets the count. Concurrent reads from the
// run loop + writes from set* handlers are guarded by Server.bpMu.
type bpMeta struct {
	Cond       expr.EvalFn // compiled condition; nil = always true
	CondText   string
	HitTarget  int    // 0 = fire every hit; N>0 = fire only on Nth hit
	Hits       int    // running count
	LogMessage string // when non-empty, log + continue instead of pause
}

// buildBPMeta compiles a SourceBreakpoint-style triple into a bpMeta.
// Returns nil when all three modifiers are empty (faster run-loop path).
func (s *Server) buildBPMeta(condText, hitText, logMessage string) (*bpMeta, error) {
	if condText == "" && hitText == "" && logMessage == "" {
		return nil, nil
	}
	m := &bpMeta{
		CondText:   condText,
		LogMessage: logMessage,
	}
	if condText != "" {
		fn, err := expr.Compile(condText, s.syms)
		if err != nil {
			return nil, fmt.Errorf("condition: %w", err)
		}
		m.Cond = fn
	}
	if hitText != "" {
		// DAP accepts integers ("5" -> fire on 5th hit) or simple
		// expressions like ">= 3". v1 handles plain ints; anything
		// else is a soft error reported back as message.
		n, err := strconv.Atoi(strings.TrimSpace(hitText))
		if err != nil {
			return nil, fmt.Errorf("hitCondition %q: expected integer", hitText)
		}
		if n < 1 {
			return nil, fmt.Errorf("hitCondition must be >= 1, got %d", n)
		}
		m.HitTarget = n
	}
	return m, nil
}

// shouldFireBP evaluates the meta for one hit. Returns:
//
//	fire=true  -> caller should emit `stopped` with reason=breakpoint
//	log non-"" -> caller should emit an `output` event with this body
//	            (caller continues regardless of fire after logging)
//
// For meta=nil the bp is a plain stop (fire=true, no log).
func (s *Server) shouldFireBP(meta *bpMeta) (fire bool, log string) {
	if meta == nil {
		return true, ""
	}
	meta.Hits++
	// HitTarget == 0 -> fire every hit. >0 -> fire only on that hit.
	if meta.HitTarget > 0 && meta.Hits != meta.HitTarget {
		return false, ""
	}
	if meta.Cond != nil {
		if meta.Cond(s.cpu, s.ram) == 0 {
			return false, ""
		}
	}
	if meta.LogMessage != "" {
		return false, s.formatLogMessage(meta.LogMessage)
	}
	return true, ""
}

// formatLogMessage expands `{expression}` placeholders by compiling each
// expression and substituting its evaluated result. Bad expressions
// render as `{!err}` so the user sees something useful in the console.
func (s *Server) formatLogMessage(msg string) string {
	var out strings.Builder
	i := 0
	for i < len(msg) {
		if msg[i] != '{' {
			out.WriteByte(msg[i])
			i++
			continue
		}
		// Find matching '}'.
		j := strings.IndexByte(msg[i+1:], '}')
		if j < 0 {
			out.WriteString(msg[i:])
			break
		}
		exprSrc := msg[i+1 : i+1+j]
		fn, err := expr.Compile(exprSrc, s.syms)
		if err != nil {
			out.WriteString("{!" + err.Error() + "}")
		} else {
			out.WriteString(formatEvalResult(fn(s.cpu, s.ram)))
		}
		i += 1 + j + 1
	}
	return out.String()
}
