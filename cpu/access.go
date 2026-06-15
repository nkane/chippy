package cpu

// Access tracking (issue #421) — an opt-in, zero-cost-when-unset hook that
// reports every CPU bus access and its kind, so a host (e.g. nessy's
// debugger) can build a Mesen-style memory access heatmap without forking the
// core. chippy itself records nothing; the host's hook owns the recency state.

// AccessKind classifies a CPU bus access.
type AccessKind uint8

const (
	// AccessRead is a data read (operand fetch, indirect pointer, dummy
	// cycle, stack pull, …).
	AccessRead AccessKind = iota
	// AccessWrite is a data write (store, stack push, RMW write-back).
	AccessWrite
	// AccessExec is an opcode fetch — the byte the CPU executed.
	AccessExec
)

func (k AccessKind) String() string {
	switch k {
	case AccessRead:
		return "read"
	case AccessWrite:
		return "write"
	case AccessExec:
		return "exec"
	default:
		return "?"
	}
}

// SetAccessHook installs (or clears, with nil) a callback invoked on every CPU
// bus access. Opcode fetches report AccessExec, data reads AccessRead, writes
// AccessWrite. The hook runs inline on the bus hot path, so keep it cheap —
// stamp an array, don't allocate. Pass nil to disable (the default), which
// removes all per-access overhead bar one not-taken branch.
func (c *CPU) SetAccessHook(fn func(addr uint16, kind AccessKind)) {
	c.accessHook = fn
}

// AccessHook returns the currently-installed access hook (nil when unset).
// Lets a caller chain its own hook in front of an existing one — e.g. the
// DAP server's dirty-memory tracker (issue #440) composing with a host's
// heatmap hook — and restore the prior hook afterward.
func (c *CPU) AccessHook() func(addr uint16, kind AccessKind) {
	return c.accessHook
}
