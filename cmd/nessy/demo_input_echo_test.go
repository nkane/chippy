package main

import (
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// Pinned framebuffer SHAs for the input-echo demo. Two states matter:
//
//   - Idle: no buttons pressed. All 8 indicator boxes render empty
//     ($30). This is the boot-state snapshot the user sees on launch.
//   - Up pressed: ButtonUp held throughout the run. The Up indicator
//     ($214D) flips to $31; everything else stays empty.
//
// Regen procedure if the PPU output legitimately changes:
//  1. Modify the source and rebuild via `make -C roms/demos`.
//  2. Run the tests; copy each "got" hash into the matching constant.
//  3. Spot-check via TestDemo_InputEcho_Inspect to confirm visuals.
const (
	inputEchoIdleSHA = "2c68a7407bcccd77b9b6b9d7c2e317f4fb2f22efa732b902665d68dcdd2b75fe"
	inputEchoUpSHA   = "f74e9ad01191369adcf006f970254d46bd18645258b7448736b6914c1554ff2b"
)

func inputEchoROMPath() string {
	return filepath.Join("..", "..", "roms", "demos", "input-echo", "input-echo.nes")
}

// TestDemo_InputEcho_Idle boots input-echo with no joypad input and
// confirms the framebuffer hash matches the empty-indicators baseline.
// 7 frames runs: 1 frame for reset's 1st vblank, 1 for the RAM clear
// + 2nd vblank, then ≥5 mainloop frames so the indicators are stable.
func TestDemo_InputEcho_Idle(t *testing.T) {
	fb := runDemoFramesWithInput(t, inputEchoROMPath(), 7, nil)
	got := hexSHA256(fb)
	if got != inputEchoIdleSHA {
		t.Fatalf("input-echo idle SHA mismatch\n  want %s\n  got  %s",
			inputEchoIdleSHA, got)
	}
}

// TestDemo_InputEcho_UpPressed boots the demo with ButtonUp held high
// for the entire run. The Up indicator (nametable $214D) should flip
// from $30 to $31, changing the framebuffer hash relative to idle.
func TestDemo_InputEcho_UpPressed(t *testing.T) {
	fb := runDemoFramesWithInput(t, inputEchoROMPath(), 7, func(bus *nesBus) {
		bus.joy.P1.Set(joypad.ButtonUp, true)
	})
	got := hexSHA256(fb)
	if got != inputEchoUpSHA {
		t.Fatalf("input-echo up-pressed SHA mismatch\n  want %s\n  got  %s",
			inputEchoUpSHA, got)
	}
	if got == inputEchoIdleSHA {
		t.Fatalf("input-echo up-pressed identical to idle — joypad path not exercised")
	}
}
