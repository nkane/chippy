package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// runDemoFrames boots an iNES ROM through buildNES and advances the
// CPU by frameCount * cpuCyclesPerFrame cycles. Headless — no Ebiten.
// Returns the framebuffer bytes for the caller to hash / inspect.
func runDemoFrames(t *testing.T, romPath string, frameCount int) []byte {
	fb, _ := runDemoFramesWithBus(t, romPath, frameCount)
	return fb
}

// runDemoFramesWithBus is the diagnostic variant — exposes the live
// nesBus so inspect tests can probe CPU + PPU state alongside the
// rendered framebuffer.
func runDemoFramesWithBus(t *testing.T, romPath string, frameCount int) ([]byte, *nesBus) {
	t.Helper()
	data, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read %s: %v", romPath, err)
	}
	rom, err := nes.ParseBytes(data)
	if err != nil {
		t.Fatalf("parse %s: %v", romPath, err)
	}
	bus, err := buildNES(rom)
	if err != nil {
		t.Fatalf("buildNES: %v", err)
	}
	// Mirror cmd/nessy's per-frame stepping — one Update() worth of
	// cycles per simulated frame. cpuCyclesPerFrame is defined in
	// game.go (tag=nessy), so we duplicate the constant locally to
	// keep this test tag-agnostic.
	const cyclesPerFrame = 29830
	target := uint64(frameCount) * cyclesPerFrame
	for bus.cpu.Cycles < target {
		if bus.cpu.Halted {
			// CPU is in a `JMP self` spin (or hit a KIL). The PPU
			// still needs to tick so the renderer captures the
			// post-init framebuffer — Step() short-circuits the
			// busTicker fan-out once Halted is true, so we drive
			// the PPU manually here. One "instruction" worth of
			// cycles per loop iteration mirrors what a JMP-self
			// would have cost (3 cycles).
			bus.ppu.Tick(3)
			bus.cpu.Cycles += 3
			continue
		}
		bus.cpu.Step()
	}
	return bus.ppu.FrameBuffer(), bus
}

// hexSHA256 hashes the bytes and returns a lowercase-hex digest so
// expected constants stay copy-pasteable.
func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestDemo_HelloBG boots roms/demos/hello-bg/hello-bg.nes, advances 5
// frames so the reset's 2-vblank warmup + the post-init renderer has
// settled, then hashes the framebuffer. Pinned SHA: any change in PPU
// rendering, palette mapping, or nametable layout shifts the hash and
// trips this test — exactly what we want for regression coverage.
//
// Regenerating the pin:
//
//  1. Update the demo's .s as needed; rebuild via `make -C roms/demos`.
//  2. Run `go test -run TestDemo_HelloBG ./cmd/nessy/... -v`; copy the
//     "actual" hash into helloBGFrameSHA.
//  3. Eyeball the screenshot in `nessy hello-bg.nes` before committing.
const helloBGFrameSHA = "4cc02c677c4d4128d442986d4d204a65d00258da3b5af6fce5ae0bf27835470e"

func TestDemo_HelloBG(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "hello-bg", "hello-bg.nes")
	fb := runDemoFrames(t, romPath, 5)
	got := hexSHA256(fb)
	if got != helloBGFrameSHA {
		t.Fatalf("hello-bg framebuffer SHA mismatch\n  want %s\n  got  %s\n"+
			"If the demo source / PPU output changed intentionally, update "+
			"helloBGFrameSHA in demo_test.go after eyeballing the rendered window.",
			helloBGFrameSHA, got)
	}
}
