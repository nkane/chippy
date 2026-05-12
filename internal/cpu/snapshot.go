package cpu

// Snapshot captures the full architectural and bookkeeping state of a CPU
// plus a copy of the RAM contents. Used by the TUI's reverse-step ring
// buffer and the DAP server's stepBack handler.
//
// Peripherals connected via MMIO snapshot themselves into the
// Peripherals map — the cpu package defers the actual serialisation to
// each peripheral (which implements peripheral.Snapshotable). The map
// is keyed by a caller-chosen string (typically the peripheral's base
// MMIO address like "$F001"). cpu.CPU.Snapshot leaves the map nil; the
// caller (TUI or DAP server) fills it before pushing to the ring.
type Snapshot struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Halted         bool

	extraCycles int
	irqLine     bool
	nmiPending  bool

	RAM [0x10000]byte

	// Peripherals: optional per-MMIO-device state. Populated by the
	// caller (TUI / DAP) at snapshot time, consumed at restore time.
	// Keys are caller-defined; values are peripheral-defined bytes.
	Peripherals map[string][]byte
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
