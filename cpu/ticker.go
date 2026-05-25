package cpu

// Ticker is the optional per-instruction tick contract for peripherals
// (or bus wrappers) that need to advance time-based state alongside
// the CPU. Step() calls Tick on the active Bus after every instruction
// with the cycle delta that just elapsed (base + extras + page-cross +
// any interrupt-service cycles when the boundary serviced one).
//
// The PPU + APU in nessy (issue #182) use this to slice the CPU's
// cycle budget into PPU dots (3 per cycle) and APU frame quarters.
//
// The interface is satisfied by MMIO (which fans out to any
// registered peripheral that also satisfies Ticker), by WBus (which
// forwards to its inner Bus), and by anything else a host wraps
// around the CPU's Bus.
type Ticker interface {
	Tick(cycles int)
}
