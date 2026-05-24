package main

import (
	"path/filepath"
	"testing"
)

// mmc1-banks toggles the universal BG color between two values
// (one from each PRG bank) every ~30 frames. Frame 5 lands inside
// the first window (bank 0, black); frame 45 lands inside the
// second window (bank 1, white). SHA inequality at those points
// is the cheapest reliable signal that the MMC1 bank-switch path
// is wired end-to-end. Don't compare against frame 60+ — the toggle
// has cycled back to bank 0 by then, producing a false negative.
func TestDemo_MMC1Banks_BankSwitchVisible(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "mmc1-banks", "mmc1-banks.nes")
	fbEarly := runDemoFrames(t, romPath, 5)
	fbLate := runDemoFrames(t, romPath, 45)
	if hexSHA256(fbEarly) == hexSHA256(fbLate) {
		t.Fatalf("mmc1-banks: framebuffer identical at frame 5 vs 45; bank switch never took effect")
	}
}
