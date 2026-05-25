package main

import (
	"path/filepath"
	"testing"
)

// mmc3-split arms the MMC3 scanline IRQ to fire ~120 lines in; the
// handler rewrites the universal BG colour blue → green. The result
// is a horizontal split: top region blue, bottom region green —
// driven by the mapper's A12-counted IRQ (#352 enables the per-line
// A12 clock; #323 is this demo).
//
// Assert a top scanline's colour differs from a bottom scanline's,
// and that each region is internally uniform (flat colour fill).
func TestDemo_MMC3Split_TopDiffersFromBottom(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "mmc3-split", "mmc3-split.nes")
	fb, _ := runDemoFramesWithBus(t, romPath, 10)

	const w = 256
	pix := func(row int) [3]byte {
		o := (row*w + 128) * 4 // sample mid-screen column
		return [3]byte{fb[o], fb[o+1], fb[o+2]}
	}
	top := pix(40)
	bottom := pix(200)
	if top == bottom {
		t.Errorf("top row 40 colour %v == bottom row 200 %v — MMC3 IRQ split didn't fire", top, bottom)
	}
	// Each region flat: two rows within the same region match.
	if pix(20) != pix(60) {
		t.Errorf("top region not uniform: row20 %v vs row60 %v", pix(20), pix(60))
	}
	if pix(180) != pix(220) {
		t.Errorf("bottom region not uniform: row180 %v vs row220 %v", pix(180), pix(220))
	}
}
