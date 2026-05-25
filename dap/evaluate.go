package dap

import (
	"encoding/json"
	"fmt"

	"github.com/nkane/chippy/expr"
)

// handleEvaluate compiles + evaluates a single expression against current
// CPU state. Routes through internal/expr — the same compiler that powers
// `:bp X if E` and `:bpr X if E` in the TUI, so the watch panel, the
// hover-evaluate tooltip, and the debug console all see identical
// semantics. Returns the result as a hex byte/word string by default; the
// raw decimal value when the client's format.hex hint is explicitly false
// and the result fits in 32 bits.
func (s *Server) handleEvaluate(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	// Evaluating an expression reads CPU + RAM. The run loop also writes
	// CPU + RAM. Racing those is a -race detector failure (and a real
	// data hazard). Refuse to evaluate while a continue is in flight;
	// the editor is expected to pause first when it wants a value.
	if s.running.Load() {
		s.sendErrorResponse(req, "CPU is running; pause first to evaluate")
		return
	}
	var args EvaluateArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad evaluate args: %v", err))
		return
	}
	if args.Expression == "" {
		s.sendErrorResponse(req, "empty expression")
		return
	}
	fn, err := expr.Compile(args.Expression, s.syms)
	if err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("compile: %v", err))
		return
	}
	v := fn(s.cpu, s.ram)

	type body struct {
		Result             string `json:"result"`
		Type               string `json:"type,omitempty"`
		VariablesReference int    `json:"variablesReference"`
	}
	s.sendResponse(req, body{
		Result: formatEvalResult(v),
		Type:   "uint32",
	})
}

// formatEvalResult chooses a hex width that matches the value's natural
// size: 1 byte renders as $XX, 2 bytes as $XXXX, larger as $XXXXXXXX.
// Keeps the watch panel readable without forcing 32-bit padding on
// register-sized expressions.
func formatEvalResult(v uint32) string {
	switch {
	case v <= 0xFF:
		return fmt.Sprintf("$%02X", v)
	case v <= 0xFFFF:
		return fmt.Sprintf("$%04X", v)
	}
	return fmt.Sprintf("$%08X", v)
}
