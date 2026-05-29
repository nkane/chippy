package ppu

import "testing"

// On NTSC, with rendering enabled, the pre-render scanline (261)
// is one dot shorter on odd frames — the PPU jumps from dot 339
// straight to (0,0) of the next frame, skipping dot 340. SMB1
// depends on this for long-horizon audio/video phase.
func TestOddFrame_DotSkipWhenRenderingEnabled(t *testing.T) {
	p := New(&fakeCart{}, nil)

	// Enable rendering (BG show bit 3). The check looks at $2001 bits
	// 3 + 4; either being set counts. The latch samples
	// renderingEnabledDelayed which lags mask by 1 PPU clock (Mesen2
	// model), so seed both directly here.
	p.mask = 0x08
	p.renderingEnabledDelayed = true
	p.scanline = preRenderScanline
	p.dot = 338
	p.frameCount = 1 // odd

	p.stepDot() // 338 -> 339: arms the skip latch from rendering state
	p.stepDot() // 339 -> skip to (0,0)

	if p.scanline != 0 || p.dot != 0 {
		t.Fatalf("odd-frame skip didn't fire: (s,d)=(%d,%d); want (0,0)", p.scanline, p.dot)
	}
	if p.frameCount != 2 {
		t.Errorf("frameCount = %d; want 2", p.frameCount)
	}
}

// Even frame: no skip. Dot advances 339 → 340.
func TestOddFrame_NoSkipOnEvenFrame(t *testing.T) {
	p := New(&fakeCart{}, nil)

	p.mask = 0x08 // BG show
	p.scanline = preRenderScanline
	p.dot = 338
	p.frameCount = 0 // even

	p.stepDot() // 338 -> 339
	p.stepDot() // 339 -> 340 (no skip on even frame)

	if p.scanline != preRenderScanline || p.dot != 340 {
		t.Fatalf("even frame: (s,d)=(%d,%d); want (%d,340)", p.scanline, p.dot, preRenderScanline)
	}
	if p.frameCount != 0 {
		t.Errorf("frameCount = %d; want 0", p.frameCount)
	}
}

// Rendering disabled: no skip even on odd frame.
func TestOddFrame_NoSkipWhenRenderingDisabled(t *testing.T) {
	p := New(&fakeCart{}, nil)

	p.mask = 0 // rendering off
	p.scanline = preRenderScanline
	p.dot = 338
	p.frameCount = 1 // odd

	p.stepDot() // 338 -> 339: latch sees rendering off
	p.stepDot() // 339 -> 340 (no skip)

	if p.scanline != preRenderScanline || p.dot != 340 {
		t.Fatalf("rendering off: (s,d)=(%d,%d); want (%d,340)", p.scanline, p.dot, preRenderScanline)
	}
}
