package main

import (
	"path/filepath"
	"testing"
)

// vblank-bounce framebuffer SHAs at two distinct frame counts.
// The position of the bouncing tile is deterministic per frame (NMI
// fires every vblank, position updates by +1 or -1 per axis), so
// every frame count → exactly one expected hash. Two pinned hashes
// give the test a clear "did it animate?" signal — they must differ.
//
// Regen procedure if intentional change:
//  1. Modify source / rebuild via `make -C roms/demos`.
//  2. Run tests; copy "got" hashes into the matching constants.
//  3. Spot-check via TestDemo_VBlankBounce_Inspect.
const (
	vblankBounceEarlySHA = "c448cd02a744337e7ac4bb15b8bf8baf6cedf3516c6e451e4f0d12e467a88d68"
	vblankBounceLateSHA  = "b7c4cc557b5c0f30e7eb31b851f7fd34ec0e5b0c86c4de51f05dabc874e3891f"
)

func vblankBounceROMPath() string {
	return filepath.Join("..", "..", "roms", "demos", "vblank-bounce", "vblank-bounce.nes")
}

// TestDemo_VBlankBounce_Early: 5 frames is enough for reset to
// complete + a couple NMI ticks. The tile has barely moved from its
// starting position (16, 14).
func TestDemo_VBlankBounce_Early(t *testing.T) {
	fb := runDemoFramesWithInput(t, vblankBounceROMPath(), 5, nil)
	got := hexSHA256(fb)
	if got != vblankBounceEarlySHA {
		t.Fatalf("vblank-bounce early SHA mismatch\n  want %s\n  got  %s",
			vblankBounceEarlySHA, got)
	}
}

// TestDemo_VBlankBounce_Late: 30 frames lets the NMI handler walk
// the tile well across the playfield. Framebuffer must differ from
// the early snapshot — proves the NMI line + NMI service path work.
func TestDemo_VBlankBounce_Late(t *testing.T) {
	fb := runDemoFramesWithInput(t, vblankBounceROMPath(), 30, nil)
	got := hexSHA256(fb)
	if got != vblankBounceLateSHA {
		t.Fatalf("vblank-bounce late SHA mismatch\n  want %s\n  got  %s",
			vblankBounceLateSHA, got)
	}
	if got == vblankBounceEarlySHA {
		t.Fatalf("vblank-bounce: early and late SHAs match — NMI animation didn't move the tile")
	}
}
