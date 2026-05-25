package main

import (
	"path/filepath"
	"testing"
)

// scroll-split draws vertical stripes, sets scroll-X=0 at the top of
// the frame, then rewrites scroll-X=8 mid-render via a cycle-timed
// busy-wait. The per-scanline renderer (#206) applies the mid-frame
// write, so the top region's stripes are offset from the bottom's.
// Assert a top scanline's pixel row differs from a bottom scanline's
// — proof the split took effect (a single uniform scroll would make
// every row identical).
func TestDemo_ScrollSplit_TopDiffersFromBottom(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "scroll-split", "scroll-split.nes")
	fb, _ := runDemoFramesWithBus(t, romPath, 10)

	const w = 256
	rowDiffers := func(a, b int) bool {
		for x := 0; x < w; x++ {
			if fb[(a*w+x)*4+0] != fb[(b*w+x)*4+0] {
				return true
			}
		}
		return false
	}
	// Row 20 (top, scroll 0) vs row 200 (bottom, post-split scroll 8).
	if !rowDiffers(20, 200) {
		t.Errorf("top row 20 identical to bottom row 200 — scroll split didn't take effect")
	}
	// Sanity: two rows in the same region (both top) should match —
	// the stripe pattern is vertically uniform within a scroll region.
	sameRegion := func(a, b int) bool {
		for x := 0; x < w; x++ {
			if fb[(a*w+x)*4+0] != fb[(b*w+x)*4+0] {
				return false
			}
		}
		return true
	}
	if !sameRegion(20, 40) {
		t.Errorf("rows 20 and 40 (same top region) differ — stripes should be vertically uniform")
	}
}
