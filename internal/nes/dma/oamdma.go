// Package dma models the NES bus-stealing DMA controllers. v0.2 ships
// the $4014 OAMDMA path that drives sprites; the DMC sample-DMA channel
// (audio side, $4011 / $4015) lands with the APU in v0.3.
package dma

import "github.com/nkane/chippy/cpu"

// PPU is the subset of the NES PPU's surface that OAMDMA needs. We
// avoid importing internal/nes/ppu directly so the dma package stays
// useful for unit-testing without dragging in the full PPU graph.
type PPU interface {
	// WriteOAM copies one byte into OAM at the PPU's internal oamAddr
	// cursor and advances it. OAMDMA calls this 256 times in sequence.
	WriteOAM(v byte)
}

// CPUBus is the subset of the CPU bus OAMDMA reads from. Matches
// cpu.Bus.Read so any *cpu.MMIO / *cpu.RAM / cpu.WBus satisfies it
// for free.
type CPUBus interface {
	Read(addr uint16) byte
}

// CPUStaller is the subset of *cpu.CPU we use to charge the stall
// cycles. CurrentCycle() drives the odd-cycle alignment penalty
// (513 vs 514). Interface-narrowing keeps tests trivial.
type CPUStaller interface {
	Stall(cycles int)
	CurrentCycle() uint64
}

// OAMDMA is the $4014 peripheral. A CPU write of byte $XX triggers a
// 256-byte copy from CPU page $XX00-$XXFF into the PPU's OAM table at
// whatever oamAddr is currently set, and stalls the CPU 513 cycles to
// match real silicon's bus-steal timing.
//
// Reads of $4014 return open-bus on real hardware; we return 0 (the
// most common observed value) — no shipping ROM reads this register
// in a way that depends on the result.
//
// Real silicon charges 513 stall cycles on even-CPU-cycle entry and
// 514 on odd-CPU-cycle entry (the bus-steal aligns on a read cycle,
// so an odd-cycle start eats one extra dummy cycle). We model that.
//
// Out of scope:
//   - Per-byte sub-cycle accounting; we batch the full 256-byte copy
//     into the Write call and report a single stall total.
//   - DMC sample-DMA contention with OAMDMA (tracked separately under
//     the cycle-accuracy hardening pass).
type OAMDMA struct {
	bus  CPUBus
	ppu  PPU
	cpu  CPUStaller
	last byte // most recently written page selector — surfaced for tests

	// Per-cycle transfer state (#372 test 4 irq_and_dma): each Step
	// either reads a byte (even counter) or writes the buffered byte
	// to OAM (odd counter), so the 256-byte copy spreads across the
	// CPU's 513/514-cycle stall instead of happening "instantly" at
	// Write. ROMs that put $4015 in the DMA source page (or other
	// peripherals with side-effecting reads) depend on the per-cycle
	// read schedule for IRQ-flag clear timing to land at the right CPU
	// cycle relative to APU frame-counter assertions.
	active   bool
	haltLeft int  // halt/align cycles remaining before read/writes begin
	counter  int  // 0..511, even = read, odd = write
	buffer   byte // read-then-write holding latch
}

// New constructs an OAMDMA peripheral. All three dependencies must be
// non-nil; the constructor doesn't enforce that since the wiring
// helpers in cmd/nessy assemble these in a known order.
func New(bus CPUBus, ppu PPU, cpuStaller CPUStaller) *OAMDMA {
	return &OAMDMA{bus: bus, ppu: ppu, cpu: cpuStaller}
}

// Range claims $4014-$4014 inclusive — the single-byte OAMDMA window.
func (d *OAMDMA) Range() (uint16, uint16) { return 0x4014, 0x4014 }

// Read returns 0. The register is write-only on real silicon; readers
// see open-bus, which most ROMs don't probe.
func (d *OAMDMA) Read(addr uint16) byte { return 0 }

// Write triggers the DMA: reads 256 bytes from CPU page page*$100
// through the bus, writes each into the PPU's OAM, and charges the
// CPU 513 stall cycles. The reads happen "instantly" from the
// emulator's perspective; the cycle cost is reported to the CPU which
// drains it on its next Step().
func (d *OAMDMA) Write(addr uint16, page byte) {
	d.last = page
	d.active = true
	d.counter = 0
	// 1 halt cycle on even-start, 2 on odd-start (real silicon's bus
	// steal aligns on a read cycle). After halt, 256 read + 256 write
	// = 512 work cycles. Total 513/514.
	d.haltLeft = 1
	stall := 513
	if d.cpu.CurrentCycle()&1 == 1 {
		stall++
		d.haltLeft = 2
	}
	d.cpu.Stall(stall)
}

// Step advances the per-cycle DMA transfer by one CPU cycle. Called by
// the CPU's stall drain so the 256 reads + 256 writes spread across the
// 513-cycle bus-steal window. Even counter values read from CPU bus
// into a 1-byte latch; odd values write the latch into PPU OAM. The
// first 1-2 stall cycles (halt + optional align) are no-ops here —
// `counter` only starts incrementing once we begin the read/write pair
// sequence. Returns true when the transfer has completed.
func (d *OAMDMA) Step() bool {
	if !d.active {
		return true
	}
	if d.haltLeft > 0 {
		d.haltLeft--
		return false
	}
	if d.counter >= 512 {
		d.active = false
		return true
	}
	if d.counter&1 == 0 {
		// Read cycle
		addr := uint16(d.last)<<8 | uint16(d.counter/2)
		d.buffer = d.bus.Read(addr)
	} else {
		// Write cycle
		d.ppu.WriteOAM(d.buffer)
	}
	d.counter++
	if d.counter >= 512 {
		d.active = false
		return true
	}
	return false
}

// LastPage returns the most recent byte written to $4014. Exposed for
// tests + introspection (e.g. a future :dma debug command); not part
// of the cpu.Peripheral contract.
func (d *OAMDMA) LastPage() byte { return d.last }

// compile-time check.
var _ cpu.Peripheral = (*OAMDMA)(nil)
