package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/expr"
	"github.com/nkane/chippy/trace"
)

// cmdFind jumps the trace-replay cursor to the next frame (dir +1) or
// previous frame (dir -1) matching an expression over the frame's
// registers/flags, e.g. `:find PC=$8042`, `:find A>0x10 && X==0`. A bare
// `:find` / `:rfind` repeats the last expression so users can sweep every
// match. The expression grammar is the same one breakpoint conditions use
// (package expr); it sees the frame's A/X/Y/P/SP/PC + flag bits. Memory
// dereferences read live RAM, which is stale during pure replay, so stick
// to register/flag predicates.
func (m *Model) cmdFind(args []string, dir int) string {
	if m.TraceReplay == nil {
		return "find: not in trace-replay mode"
	}
	src := strings.TrimSpace(strings.Join(args, " "))
	if src == "" {
		src = m.lastFind
	}
	if src == "" {
		return "usage: :find PC=$XXXX | A=$NN | <expr>   (:rfind searches backward)"
	}
	pred, err := m.framePredicate(src)
	if err != nil {
		return fmt.Sprintf("find: %v", err)
	}
	m.lastFind = src
	idx, ok := m.TraceReplay.FindFunc(pred, m.TraceReplay.Index, dir)
	if !ok {
		word := "after"
		if dir < 0 {
			word = "before"
		}
		return fmt.Sprintf("find: no match %s frame %d (%q)", word, m.TraceReplay.Index+1, src)
	}
	m.TraceReplay.Index = idx
	m.applyTraceFrame()
	return fmt.Sprintf("find -> frame %d/%d (%q)", idx+1, m.TraceReplay.Len(), src)
}

// cmdCycle jumps the replay cursor to the first frame at or after an
// absolute cycle count via binary search (O(log N), issue #391).
func (m *Model) cmdCycle(args []string) string {
	if m.TraceReplay == nil {
		return "cycle: not in trace-replay mode"
	}
	if len(args) == 0 {
		return "usage: :cycle N"
	}
	n, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Sprintf("cycle: bad number %q", args[0])
	}
	ok := m.TraceReplay.SeekCycle(n)
	m.applyTraceFrame()
	f, _ := m.TraceReplay.Current()
	if !ok {
		return fmt.Sprintf("cycle %d past end -> frame %d/%d (CYC:%d)",
			n, m.TraceReplay.Index+1, m.TraceReplay.Len(), f.Cycles)
	}
	return fmt.Sprintf("cycle %d -> frame %d/%d (CYC:%d)",
		n, m.TraceReplay.Index+1, m.TraceReplay.Len(), f.Cycles)
}

// framePredicate compiles a `:find` expression into a per-frame test. Each
// frame's registers are loaded into a scratch CPU so the existing expr
// evaluator can run unchanged; a non-zero result counts as a match.
func (m *Model) framePredicate(src string) (func(trace.Frame) bool, error) {
	fn, err := expr.Compile(normalizeFindExpr(src), m.Syms)
	if err != nil {
		return nil, err
	}
	var scratch cpu.CPU
	bus := m.evalBus()
	return func(f trace.Frame) bool {
		scratch.A, scratch.X, scratch.Y = f.A, f.X, f.Y
		scratch.P, scratch.SP, scratch.PC = f.P, f.SP, f.PC
		return fn(&scratch, bus) != 0
	}, nil
}

// normalizeFindExpr lets `:find` accept a single `=` as equality (the form
// the issue + most users type: `PC=$8042`) by rewriting a bare `=` to `==`
// for the expr grammar, while leaving the two-char operators `==`, `!=`,
// `<=`, `>=` untouched.
func normalizeFindExpr(src string) string {
	var b strings.Builder
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == '=' {
			prev := byte(0)
			if i > 0 {
				prev = src[i-1]
			}
			next := byte(0)
			if i+1 < len(src) {
				next = src[i+1]
			}
			// Part of ==, !=, <=, >= -> emit verbatim.
			if prev == '=' || prev == '!' || prev == '<' || prev == '>' || next == '=' {
				b.WriteByte(c)
				continue
			}
			b.WriteString("==")
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// evalBus picks the bus expr should read memory through. Prefer the MMIO
// wrapper so the few memory-referencing predicates see the same view the
// live debugger would; nil is fine for register-only expressions.
func (m *Model) evalBus() cpu.Bus {
	switch {
	case m.WBus != nil:
		return m.WBus
	case m.CPU != nil && m.CPU.Bus != nil:
		return m.CPU.Bus
	default:
		return m.RAM
	}
}
