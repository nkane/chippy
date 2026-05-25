package ppu

import "testing"

// scrollFromV folds the fine-X latch (p.x) into the derived scroll
// — a $2006 scroll change leaves p.x set by the prior $2005 write,
// so the effective horizontal scroll is coarseX*8 + fineX (#282).
func TestScrollFromV_FoldsFineX(t *testing.T) {
	p := New(&fakeCart{}, nil)

	// coarseX = 2 (v bits 0-4), fine-X latch = 5.
	p.v = 0x0002
	p.x = 5
	p.scrollFromV()
	if got := p.scrollX; got != 2*8+5 {
		t.Errorf("scrollX = %d; want %d (coarseX*8 + fineX)", got, 2*8+5)
	}

	// fine-X 0 → pure coarse, unchanged from the old behaviour (this
	// is why the static demos keep their pinned SHAs).
	p.v = 0x0003
	p.x = 0
	p.scrollFromV()
	if got := p.scrollX; got != 3*8 {
		t.Errorf("scrollX with fineX=0 = %d; want %d", got, 3*8)
	}
}

// Sub-tile horizontal scroll: column 0 is the transparent tile $00
// (black), columns 1+ are tile $01 (white). At fine-X scroll 3, a
// screen pixel near the tile boundary that was black at scroll 0
// slides into the white tile — proof the fine-X bit slide reaches
// the rendered pixels, not just coarse 8-pixel steps.
func TestRenderScanline_FineXBitSlide(t *testing.T) {
	p := twoTilePPU(t)
	bgR, _, _ := paletteRGB(0x0F)
	whiteR, _, _ := paletteRGB(0x30)

	// Scroll 0: screen x=5 maps to effX=5 → still inside tile $00 → black.
	snap := p.frameStartScroll
	snap.scrollX = 0
	p.renderScanline(64, snap)
	if got := p.frame[(64*ScreenWidth+5)*4+0]; got != bgR {
		t.Fatalf("scroll 0 x=5 should be black (tile $00); got R=$%02X", got)
	}

	// Scroll 3 (fine-X): screen x=5 maps to effX=8 → tile $01 → white.
	snap.scrollX = 3
	p.renderScanline(64, snap)
	if got := p.frame[(64*ScreenWidth+5)*4+0]; got != whiteR {
		t.Errorf("fine-X scroll 3 x=5 should slide into tile $01 (white); got R=$%02X", got)
	}
}
