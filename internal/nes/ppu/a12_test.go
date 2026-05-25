package ppu_test

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
	"github.com/nkane/chippy/internal/nes/cart"
	"github.com/nkane/chippy/internal/nes/ppu"
)

// countingSink counts MMC3 IRQ-source assertions + acks each so the
// per-scanline ticks register as distinct fires (the cart latches
// pending until acked).
type countingSink struct{ asserts int }

func (s *countingSink) AssertIRQSource(string) { s.asserts++ }
func (s *countingSink) ClearIRQSource(string)  {}

func newMMC3PPU(t *testing.T) (*ppu.PPU, *cart.MMC3, *countingSink) {
	t.Helper()
	rom := &nes.ROM{
		Mapper:    4,
		PRG:       make([]byte, 32*1024),
		CHR:       make([]byte, 8*1024),
		Mirroring: nes.MirrorHorizontal,
	}
	c, err := cart.NewMMC3(rom)
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	sink := &countingSink{}
	c.SetIRQSink(sink)
	p := ppu.New(c, nil)
	return p, c, sink
}

// With rendering on + BG pattern table at $0000, the per-scanline
// dummy sprite-pattern fetch (#352) drives one A12 rising edge per
// scanline even with zero sprites in OAM — so the MMC3 scanline IRQ
// fires repeatedly across a frame. Before the dummy fetch this was
// 0 (A12 never rose without in-range sprites).
func TestA12_MMC3ScanlineIRQFiresWithoutSprites(t *testing.T) {
	p, c, sink := newMMC3PPU(t)
	c.CPUWrite(0xC000, 8) // IRQ latch = 8 scanlines
	c.CPUWrite(0xC001, 0) // reload
	c.CPUWrite(0xE001, 0) // IRQ enable

	p.Write(0x2000, 0x00) // BG pattern table $0000 (A12 low during BG fetch)
	p.Write(0x2001, 0x08) // BG show → rendering enabled

	// Step a full NTSC frame of dots. The dummy fetch acks happen via
	// the sink; re-arm each fire so the counter keeps cycling.
	for range nes.NTSC.DotsPerScanline * nes.NTSC.ScanlinesPerFrame {
		before := sink.asserts
		p.Tick(1) // 1 CPU cycle = 3 dots
		if sink.asserts > before {
			c.CPUWrite(0xE000, 0) // ack (clears pending)
			c.CPUWrite(0xE001, 0) // re-enable
		}
	}
	// ~240 visible scanlines / latch 8 ≈ 30 fires; allow slack.
	if sink.asserts < 20 {
		t.Errorf("MMC3 scanline IRQ fired %d times across a frame; want >= 20 (per-scanline A12)", sink.asserts)
	}
}

// Rendering disabled → no fetches → no A12 edges → no IRQ.
func TestA12_NoIRQWhenRenderingOff(t *testing.T) {
	p, c, sink := newMMC3PPU(t)
	c.CPUWrite(0xC000, 8)
	c.CPUWrite(0xC001, 0)
	c.CPUWrite(0xE001, 0)
	// mask left 0 → rendering off.
	for range nes.NTSC.DotsPerScanline * nes.NTSC.ScanlinesPerFrame {
		p.Tick(1)
	}
	if sink.asserts != 0 {
		t.Errorf("MMC3 IRQ fired %d times with rendering off; want 0", sink.asserts)
	}
}
