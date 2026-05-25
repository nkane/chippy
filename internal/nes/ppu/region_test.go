package ppu

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// After SetRegion(PAL) the PPU wraps the frame at 312 scanlines, not
// 262 — confirms the geometry actually drives stepDot, not just the
// stored field.
func TestRegion_PALFrameLength(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.SetRegion(nes.PAL)
	// Start clean at the top of a frame (SetRegion seeds the cursor
	// at the pre-render line). Step a full PAL frame's worth of dots
	// (341 * 312) and confirm one frame elapsed + we're back at the
	// start.
	p.scanline, p.dot, p.frameCount = 0, 0, 0
	dots := nes.PAL.DotsPerScanline * nes.PAL.ScanlinesPerFrame
	for range dots {
		p.stepDot()
	}
	if p.frameCount != 1 {
		t.Errorf("PAL frameCount after one frame = %d; want 1", p.frameCount)
	}
	if p.scanline != 0 || p.dot != 0 {
		t.Errorf("PAL frame didn't wrap cleanly: (s,d)=(%d,%d)", p.scanline, p.dot)
	}
}

// NTSC default still wraps at 262 — region change is opt-in.
func TestRegion_NTSCDefaultFrameLength(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.scanline, p.dot, p.frameCount = 0, 0, 0
	dots := nes.NTSC.DotsPerScanline * nes.NTSC.ScanlinesPerFrame
	for range dots {
		p.stepDot()
	}
	if p.frameCount != 1 {
		t.Errorf("NTSC frameCount after one frame = %d; want 1", p.frameCount)
	}
}

// vblank flag still sets at scanline 241 under PAL (shared across
// regions) — a PAL game's NMI wait loop still works.
func TestRegion_PALVBlankAt241(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.SetRegion(nes.PAL)
	p.scanline, p.dot, p.frameCount = 0, 0, 0
	// Step until just past scanline 241 dot 1.
	for p.scanline != nes.PAL.VBlankScanline || p.dot < 2 {
		p.stepDot()
		if p.frameCount > 0 {
			t.Fatal("wrapped frame before reaching vblank")
		}
	}
	if p.status&0x80 == 0 {
		t.Errorf("vblank flag not set at PAL scanline 241")
	}
}
