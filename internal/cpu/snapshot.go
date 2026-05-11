package cpu

// Snapshot captures the full architectural and bookkeeping state of a CPU
// plus a copy of the RAM contents. Used by the TUI's reverse-step ring
// buffer.
//
// Caveat: peripherals connected via MMIO are NOT snapshotted — only RAM is.
// Reverse-stepping across a peripheral side-effect (e.g. a keyboard register
// drain) won't unwind the peripheral. Acceptable in v1 because real
// reverse-debugging sessions rarely span peripheral interactions; programs
// that do should pause the CPU first or use a longer ring with periodic
// peripheral snapshots in a follow-up.
type Snapshot struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Halted         bool

	extraCycles int
	irqLine     bool
	nmiPending  bool

	RAM [0x10000]byte
}

// Snapshot returns a full state capture of the CPU + the supplied RAM. ram
// must be the same backing store the CPU's Bus eventually resolves to —
// snapshots aren't useful if MMIO wraps a different RAM.
func (c *CPU) Snapshot(ram *RAM) Snapshot {
	s := Snapshot{
		A: c.A, X: c.X, Y: c.Y, SP: c.SP, P: c.P,
		PC:          c.PC,
		Cycles:      c.Cycles,
		Halted:      c.Halted,
		extraCycles: c.extraCycles,
		irqLine:     c.irqLine,
		nmiPending:  c.nmiPending,
	}
	s.RAM = ram.Data
	return s
}

// Restore writes the snapshot back into the CPU and RAM. The opcodes pointer
// is left alone — Variant doesn't change at runtime, so the table binding
// from New/Reset remains valid.
func (c *CPU) Restore(s Snapshot, ram *RAM) {
	c.A, c.X, c.Y, c.SP, c.P = s.A, s.X, s.Y, s.SP, s.P
	c.PC = s.PC
	c.Cycles = s.Cycles
	c.Halted = s.Halted
	c.extraCycles = s.extraCycles
	c.irqLine = s.irqLine
	c.nmiPending = s.nmiPending
	ram.Data = s.RAM
}
