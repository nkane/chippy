// Package dma models the NES bus-stealing DMA controllers. v0.2 ships
// the $4014 OAMDMA path that drives sprites; the DMC sample-DMA channel
// (audio side, $4011 / $4015) lands with the APU in v0.3.
package dma

import "github.com/nkane/chippy/cpu"

// SpriteDmaSink is the slice of *cpu.CPU OAMDMA needs to kick a
// transfer. The CPU's ProcessPendingDma loop owns all the bus-level
// work (halt + 256 read/write pairs + alignment); OAMDMA just hands
// the source page across.
type SpriteDmaSink interface {
	SetNeedSpriteDma(page byte)
}

// OAMDMA is the $4014 peripheral. A CPU write of byte $XX flags a
// pending 256-byte sprite-DMA transfer from CPU page $XX00-$XXFF; the
// actual cycle-by-cycle bus-steal runs inside cpu.ProcessPendingDma on
// the next read so the 513/514-cycle window lands at the right CPU
// cycle parity.
//
// Reads of $4014 return open-bus on real hardware; we return 0 (the
// most common observed value) — no shipping ROM reads this register
// in a way that depends on the result.
type OAMDMA struct {
	cpu  SpriteDmaSink
	last byte // most recently written page selector — surfaced for tests
}

// New constructs an OAMDMA peripheral. The CPU sink must be non-nil
// or Write will panic; the constructor doesn't enforce that since
// the wiring helper in cmd/nessy assembles dependencies in a known
// order.
func New(cpuSink SpriteDmaSink) *OAMDMA {
	return &OAMDMA{cpu: cpuSink}
}

// Range claims $4014-$4014 inclusive — the single-byte OAMDMA window.
func (d *OAMDMA) Range() (uint16, uint16) { return 0x4014, 0x4014 }

// Read returns 0. The register is write-only on real silicon; readers
// see open-bus, which most ROMs don't probe.
func (d *OAMDMA) Read(addr uint16) byte { return 0 }

// Write flags the pending sprite-DMA transfer. The CPU drains the
// 513/514-cycle window on its next read via ProcessPendingDma (#376).
func (d *OAMDMA) Write(addr uint16, page byte) {
	d.last = page
	d.cpu.SetNeedSpriteDma(page)
}

// LastPage returns the most recent byte written to $4014. Exposed for
// tests + introspection (e.g. a future :dma debug command); not part
// of the cpu.Peripheral contract.
func (d *OAMDMA) LastPage() byte { return d.last }

// compile-time check.
var _ cpu.Peripheral = (*OAMDMA)(nil)
