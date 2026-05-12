package tui

import (
	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/expr"
	"github.com/nkane/chippy/internal/symbols"
)

// condFn is the bool-returning evaluator the breakpoint check loop calls.
// The actual expression compiler lives in internal/expr so the DAP server
// can share it without depending on tui.
type condFn func(*cpu.CPU, cpu.Bus) bool

func compileCondition(src string, syms *symbols.Table) (condFn, error) {
	fn, err := expr.Compile(src, syms)
	if err != nil {
		return nil, err
	}
	return func(c *cpu.CPU, bus cpu.Bus) bool {
		return fn(c, bus) != 0
	}, nil
}
