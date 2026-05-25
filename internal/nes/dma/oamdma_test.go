package dma

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

// fakePPU records every OAM write in order. Mirrors the surface
// OAMDMA actually depends on without dragging the full PPU graph in.
type fakePPU struct {
	oam []byte
}

func (p *fakePPU) WriteOAM(v byte) { p.oam = append(p.oam, v) }

// fakeStaller records the cumulative stall count and reports the
// emulated CPU cycle (configurable for odd-cycle tests).
type fakeStaller struct {
	stalled int
	cycle   uint64
}

func (s *fakeStaller) Stall(cycles int)     { s.stalled += cycles }
func (s *fakeStaller) CurrentCycle() uint64 { return s.cycle }

// A $4014 write copies the named CPU page into OAM verbatim and
// stalls the CPU 513 cycles.
func TestOAMDMA_WriteCopiesPageAndStalls(t *testing.T) {
	ram := cpu.NewRAM()
	// Seed page $02 with a recognizable pattern.
	for i := range 256 {
		ram.Write(0x0200+uint16(i), byte(i^0x5A))
	}
	pp := &fakePPU{}
	st := &fakeStaller{}
	d := New(ram, pp, st)

	d.Write(0x4014, 0x02)

	if len(pp.oam) != 256 {
		t.Fatalf("oam writes = %d; want 256", len(pp.oam))
	}
	for i := range 256 {
		want := byte(i ^ 0x5A)
		if pp.oam[i] != want {
			t.Fatalf("oam[%d] = $%02X; want $%02X", i, pp.oam[i], want)
		}
	}
	if st.stalled != 513 {
		t.Fatalf("stalled cycles = %d; want 513", st.stalled)
	}
	if d.LastPage() != 0x02 {
		t.Fatalf("LastPage = $%02X; want $02", d.LastPage())
	}
}

// Reads of $4014 return zero (open-bus stub). No state mutation.
func TestOAMDMA_ReadReturnsZero(t *testing.T) {
	d := New(cpu.NewRAM(), &fakePPU{}, &fakeStaller{})
	if v := d.Read(0x4014); v != 0 {
		t.Fatalf("Read = $%02X; want $00", v)
	}
}

// Range claims exactly $4014. The single-byte window matters for
// MMIO registration alongside the cart ($4020-$FFFF) and joypad
// ($4016-$4017).
func TestOAMDMA_Range(t *testing.T) {
	d := New(cpu.NewRAM(), &fakePPU{}, &fakeStaller{})
	lo, hi := d.Range()
	if lo != 0x4014 || hi != 0x4014 {
		t.Fatalf("Range = $%04X-$%04X; want $4014-$4014", lo, hi)
	}
}

// OAMDMA reads exactly $XX00-$XXFF — never overruns into the
// following page. Seeds page $02 with one value and page $03 with a
// different value; after a DMA from $02, OAM must contain only the
// $02 values. Catches off-by-one bus.Read addressing.
func TestOAMDMA_ReadsExactly256BytesNoOverrun(t *testing.T) {
	ram := cpu.NewRAM()
	for i := range 256 {
		ram.Write(0x0200+uint16(i), 0x11)
		ram.Write(0x0300+uint16(i), 0x22)
	}
	pp := &fakePPU{}
	d := New(ram, pp, &fakeStaller{})

	d.Write(0x4014, 0x02)

	if len(pp.oam) != 256 {
		t.Fatalf("oam writes = %d; want exactly 256", len(pp.oam))
	}
	for i, b := range pp.oam {
		if b != 0x11 {
			t.Fatalf("oam[%d] = $%02X; want $11 (no over-read into page $03)", i, b)
		}
	}
}

// End-to-end through MMIO: register the peripheral, write through
// the bus, observe both side effects (OAM populated + stall queued).
// Catches register-order bugs in cmd/nessy/wiring.go.
func TestOAMDMA_ThroughMMIO(t *testing.T) {
	ram := cpu.NewRAM()
	for i := range 256 {
		ram.Write(0x0300+uint16(i), byte(0xAA))
	}
	mmio := cpu.NewMMIO(ram)
	pp := &fakePPU{}
	processor := cpu.New(mmio)

	d := New(mmio, pp, processor)
	if err := mmio.Register(d); err != nil {
		t.Fatalf("register: %v", err)
	}

	mmio.Write(0x4014, 0x03)

	if len(pp.oam) != 256 {
		t.Fatalf("oam writes = %d; want 256", len(pp.oam))
	}
	for i, b := range pp.oam {
		if b != 0xAA {
			t.Fatalf("oam[%d] = $%02X; want $AA", i, b)
		}
	}
	// Stall queued — first Step drains the stall without executing an
	// opcode. Real silicon: 513 cycles on even-CPU-cycle entry, 514
	// on odd. cpu.Reset() leaves Cycles=7 (odd), so this path expects
	// 514.
	wantStall := 513
	if processor.Cycles&1 == 1 {
		wantStall = 514
	}
	preCycles := processor.Cycles
	got := processor.Step()
	if got != wantStall {
		t.Fatalf("post-DMA Step cycles = %d; want %d", got, wantStall)
	}
	if processor.Cycles != preCycles+uint64(wantStall) {
		t.Fatalf("CPU.Cycles delta = %d; want %d", processor.Cycles-preCycles, wantStall)
	}
}

// Odd-CPU-cycle entry adds a dummy cycle: 513 → 514. The penalty
// matches the bus-steal alignment behaviour of real silicon (the
// nesdev "1+513" vs "2+512" framing).
func TestOAMDMA_OddCycleAddsOne(t *testing.T) {
	ram := cpu.NewRAM()
	pp := &fakePPU{}
	st := &fakeStaller{cycle: 7} // odd
	d := New(ram, pp, st)

	d.Write(0x4014, 0x00)

	if st.stalled != 514 {
		t.Fatalf("odd-entry stall = %d; want 514", st.stalled)
	}

	st = &fakeStaller{cycle: 8} // even
	d = New(ram, pp, st)
	d.Write(0x4014, 0x00)
	if st.stalled != 513 {
		t.Fatalf("even-entry stall = %d; want 513", st.stalled)
	}
}
