package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// TestDemo_HelloBG_Inspect (skipped by default — guarded with
// CHIPPY_DEMO_INSPECT env) renders the framebuffer and prints
// summary stats + a textual screenshot so we can eyeball the demo
// before pinning helloBGFrameSHA. Run with:
//
//	CHIPPY_DEMO_INSPECT=1 go test -run TestDemo_HelloBG_Inspect -v ./cmd/nessy/...
//
// TestDemo_InputEcho_Inspect renders the input-echo demo with a
// caller-selected button held (CHIPPY_DEMO_BUTTON env), prints
// summary stats + a textual screenshot. Skipped without
// CHIPPY_DEMO_INSPECT. Examples:
//
//	CHIPPY_DEMO_INSPECT=1 go test -run TestDemo_InputEcho_Inspect -v ./cmd/nessy/...
//	CHIPPY_DEMO_INSPECT=1 CHIPPY_DEMO_BUTTON=Up go test ...
func TestDemo_InputEcho_Inspect(t *testing.T) {
	if os.Getenv("CHIPPY_DEMO_INSPECT") == "" {
		t.Skip("set CHIPPY_DEMO_INSPECT=1 to render this demo")
	}
	romPath := filepath.Join("..", "..", "roms", "demos", "input-echo", "input-echo.nes")
	setup := func(bus *nesBus) {
		switch os.Getenv("CHIPPY_DEMO_BUTTON") {
		case "Up":
			bus.joy.P1.Set(joypad.ButtonUp, true)
		case "Down":
			bus.joy.P1.Set(joypad.ButtonDown, true)
		case "Left":
			bus.joy.P1.Set(joypad.ButtonLeft, true)
		case "Right":
			bus.joy.P1.Set(joypad.ButtonRight, true)
		case "A":
			bus.joy.P1.Set(joypad.ButtonA, true)
		case "B":
			bus.joy.P1.Set(joypad.ButtonB, true)
		case "Select":
			bus.joy.P1.Set(joypad.ButtonSelect, true)
		case "Start":
			bus.joy.P1.Set(joypad.ButtonStart, true)
		}
	}
	fb, _ := runDemoFramesInternal(t, romPath, 7, setup)
	seen := map[[3]byte]int{}
	for i := 0; i < len(fb); i += 4 {
		k := [3]byte{fb[i], fb[i+1], fb[i+2]}
		seen[k]++
	}
	t.Logf("distinct RGB triplets: %d", len(seen))
	for k, n := range seen {
		t.Logf("  %02X %02X %02X  x%d", k[0], k[1], k[2], n)
	}
	t.Log("rough ASCII map (top half of screen):")
	for y := 0; y < 240; y += 4 {
		var row [64]byte
		for x := 0; x < 256; x += 4 {
			off := (y*256 + x) * 4
			if int(fb[off])+int(fb[off+1])+int(fb[off+2]) > 400 {
				row[x/4] = '#'
			} else {
				row[x/4] = '.'
			}
		}
		t.Log(string(row[:]))
	}
}

func TestDemo_HelloBG_Inspect(t *testing.T) {
	if os.Getenv("CHIPPY_DEMO_INSPECT") == "" {
		t.Skip("set CHIPPY_DEMO_INSPECT=1 to render this demo")
	}
	romPath := filepath.Join("..", "..", "roms", "demos", "hello-bg", "hello-bg.nes")
	fb, bus := runDemoFramesWithBus(t, romPath, 5)
	t.Logf("CPU  PC=%04X A=%02X X=%02X Y=%02X P=%02X SP=%02X Cycles=%d Halted=%v",
		bus.cpu.PC, bus.cpu.A, bus.cpu.X, bus.cpu.Y, bus.cpu.P, bus.cpu.SP, bus.cpu.Cycles, bus.cpu.Halted)
	t.Logf("PPU  status=%02X scanline=%d dot=%d frames=%d",
		bus.ppu.Status(), bus.ppu.Scanline(), bus.ppu.Dot(), bus.ppu.FrameCount())

	// Distinct RGB triplets in the framebuffer — small set if rendering
	// is sane (universal bg + a couple palette colors).
	seen := map[[3]byte]int{}
	for i := 0; i < len(fb); i += 4 {
		k := [3]byte{fb[i], fb[i+1], fb[i+2]}
		seen[k]++
	}
	t.Logf("distinct RGB triplets: %d", len(seen))
	for k, n := range seen {
		t.Logf("  %02X %02X %02X  x%d", k[0], k[1], k[2], n)
	}

	// Textual screenshot of the middle 4 nametable rows (8 pixel-rows
	// each, sampled every 8px). Useful sanity check that the "HELLO
	// NESSY" string lands on the expected nametable row.
	t.Log("rough ASCII map (top half of screen):")
	for y := 0; y < 240; y += 4 {
		var row [64]byte
		for x := 0; x < 256; x += 4 {
			off := (y*256 + x) * 4
			// Bright pixel (palette color 1) → '#'; dark → '.'.
			if int(fb[off])+int(fb[off+1])+int(fb[off+2]) > 400 {
				row[x/4] = '#'
			} else {
				row[x/4] = '.'
			}
		}
		t.Log(string(row[:]))
	}
}
