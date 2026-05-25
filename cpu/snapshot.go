package cpu

// Snapshot captures the architectural and bookkeeping state of a CPU
// plus enough RAM delta to undo one execution unit. Used by the TUI's
// reverse-step ring buffer and the DAP server's stepBack handler.
//
// RAM is stored as page-level deltas (Pages: page-index → before-image
// of that 256-byte page). To restore: copy each page's image back. A
// typical instruction touches 0–1 pages, so a snapshot costs ~hundreds
// of bytes instead of 64 KiB. Worst case (an instruction that somehow
// rewrites every page) collapses back to ~64 KiB.
//
// Capture protocol — two-phase, caller-driven, so a single snapshot
// can wrap a multi-step sweep (next-over-JSR, runToLine):
//
//  1. snap := cpu.Snapshot(ram)      // regs BEFORE the step
//  2. (capture peripherals; the caller fills snap.Peripherals)
//  3. ram.ResetShadow()              // start a fresh page-delta epoch
//  4. cpu.Step() / multi-step sweep  // writes populate the shadow
//  5. snap.Pages = ram.TakeShadow()  // claim the before-images
//  6. ring.Push(snap)
//
// Peripherals connected via MMIO snapshot themselves into the
// Peripherals map — the cpu package defers the actual serialisation to
// each peripheral (which implements peripheral.Snapshotable). Keys are
// caller-defined; cpu.CPU.Snapshot leaves the map nil.
type Snapshot struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Halted         bool

	extraCycles int
	irqLine     bool
	nmiPending  bool

	// Pages: page-index → before-image of that 256-byte page. Apply
	// each page back to RAM at restore time to undo writes performed
	// during the snapshot's epoch.
	Pages map[byte][256]byte

	// Peripherals: optional per-MMIO-device state. Populated by the
	// caller (TUI / DAP) at snapshot time, consumed at restore time.
	// Keys are caller-defined; values are peripheral-defined bytes.
	Peripherals map[string][]byte
}

// Snapshot returns a regs-only state capture. Page deltas and
// peripheral state are filled in by the caller after Step using
// ram.TakeShadow() — see the capture protocol on Snapshot.
func (c *CPU) Snapshot(ram *RAM) Snapshot {
	return Snapshot{
		A: c.A, X: c.X, Y: c.Y, SP: c.SP, P: c.P,
		PC:          c.PC,
		Cycles:      c.Cycles,
		Halted:      c.Halted,
		extraCycles: c.extraCycles,
		irqLine:     c.irqLine,
		nmiPending:  c.nmiPending,
	}
}

// Restore writes the snapshot back into the CPU and applies any page
// deltas back to RAM. The opcodes pointer is left alone — Variant
// doesn't change at runtime, so the table binding from New/Reset
// remains valid. The shadow epoch is reset because the writes Restore
// just performed are not part of any user-visible step.
func (c *CPU) Restore(s Snapshot, ram *RAM) {
	c.A, c.X, c.Y, c.SP, c.P = s.A, s.X, s.Y, s.SP, s.P
	c.PC = s.PC
	c.Cycles = s.Cycles
	c.Halted = s.Halted
	c.extraCycles = s.extraCycles
	c.irqLine = s.irqLine
	c.nmiPending = s.nmiPending
	for page, img := range s.Pages {
		base := int(page) << 8
		copy(ram.Data[base:base+256], img[:])
	}
	ram.ResetShadow()
}
