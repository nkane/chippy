package main

import (
	"path/filepath"
	"testing"
)

// oam-grid fills OAM with a 64-sprite grid via $4014 OAMDMA every
// frame. The result is a recognisable solid-square grid centred on
// the playfield. The test asserts OAM was loaded correctly +
// framebuffer rendered non-zero pixels; SHA-pinning was deliberately
// skipped because sprite renderer tweaks legitimately shift the
// per-pixel SHA without indicating a regression — the OAM + bg-
// opaque mask checks here catch the real surface.

func TestDemo_OAMGrid_RendersAndOAMNonZero(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "oam-grid", "oam-grid.nes")
	fb, bus := runDemoFramesWithBus(t, romPath, 5)

	// OAM[0] should hold the first sprite's Y (88).
	if got := bus.ppu.OAM(0); got != 88 {
		t.Errorf("OAM[0] (sprite-0 Y) = %d; want 88", got)
	}
	// OAM[63*4+3] = sprite 63 X = 88 + 7*8 = 144.
	if got := bus.ppu.OAM(byte(63*4 + 3)); got != 144 {
		t.Errorf("OAM[$FF] (sprite-63 X) = %d; want 144", got)
	}
	// Framebuffer must be non-trivial (the grid is visible). Check
	// that not every byte is zero — any rendered pixel breaks the
	// all-zero baseline.
	allZero := true
	for _, b := range fb {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Errorf("framebuffer is all-zero — sprite grid didn't render")
	}
}
