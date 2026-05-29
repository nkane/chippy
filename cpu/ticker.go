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

// StallStepper is the per-cycle callback fired during stall drains
// (e.g. OAMDMA bus-steal). The peripheral that owns the stall (set via
// CPU.SetStallStepper after wiring) runs one cycle of its DMA state
// machine per call — typically a read on even counter or a write on
// odd, so the 256-byte transfer spreads across the 513-514-cycle window
// like Mesen2 does. Returns true when the transfer is complete.
type StallStepper interface {
	Step() bool
}

// PPURunner is the master-clock-deadline contract for advancing the PPU
// in lockstep with the CPU's per-cycle interleave (#372 redesign).
// chippy's NES read/write splits the cycle's master-clock budget around
// the bus access (5 mc before a read, 7 after; 7 before a write, 5
// after) and calls Run(masterClockDeadline - ppuOffset) at each split
// so the PPU advances dot-by-dot up to that deadline. Mirrors Mesen2
// NesPpu::Run, which is the implementation passes both Blargg accuracy
// suites cpu_interrupts_v2 and ppu_vbl_nmi simultaneously.
type PPURunner interface {
	Run(masterClockDeadline uint64)
}
