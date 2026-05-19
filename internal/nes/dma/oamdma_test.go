package dma

import (
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

// fakePPU records every OAM write in order. Mirrors the surface
// OAMDMA actually depends on without dragging the full PPU graph in.
type fakePPU struct {
	oam []byte
}

func (p *fakePPU) WriteOAM(v byte) { p.oam = append(p.oam, v) }

// fakeStaller records the cumulative stall count.
type fakeStaller struct{ stalled int }

func (s *fakeStaller) Stall(cycles int) { s.stalled += cycles }

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
	// Stall queued — first Step should drain 513 cycles without
	// executing an opcode.
	preCycles := processor.Cycles
	got := processor.Step()
	if got != 513 {
		t.Fatalf("post-DMA Step cycles = %d; want 513", got)
	}
	if processor.Cycles != preCycles+513 {
		t.Fatalf("CPU.Cycles delta = %d; want 513", processor.Cycles-preCycles)
	}
}
