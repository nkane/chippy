// Package dma models the NES bus-stealing DMA controllers. v0.2 ships
// the $4014 OAMDMA path that drives sprites; the DMC sample-DMA channel
// (audio side, $4011 / $4015) lands with the APU in v0.3.
package dma

import "github.com/nkane/chippy/internal/cpu"

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
	base := uint16(page) << 8
	for i := range 256 {
		b := d.bus.Read(base + uint16(i))
		d.ppu.WriteOAM(b)
	}
	stall := 513
	if d.cpu.CurrentCycle()&1 == 1 {
		stall++
	}
	d.cpu.Stall(stall)
}

// LastPage returns the most recent byte written to $4014. Exposed for
// tests + introspection (e.g. a future :dma debug command); not part
// of the cpu.Peripheral contract.
func (d *OAMDMA) LastPage() byte { return d.last }

// compile-time check.
var _ cpu.Peripheral = (*OAMDMA)(nil)
