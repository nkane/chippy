//go:build nessy

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// End-to-end save / load. Boots a deterministic demo (vblank-bounce,
// which mutates VRAM + OAM every NMI), runs it for N frames, saves
// state. Then runs the same ROM fresh for N frames + loads the
// saved state on top. The two buses should produce bit-identical
// framebuffer + CPU PC + OAM after one more frame of stepping —
// proof that the full state surface is captured.
func TestSaveState_RoundTrip_EndToEnd(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "vblank-bounce", "vblank-bounce.nes")
	romBytes, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read rom: %v", err)
	}
	rom, err := nes.ParseBytes(romBytes)
	if err != nil {
		t.Fatalf("parse rom: %v", err)
	}

	// Reference run: advance to frame 60, capture state.
	ref, err := buildNES(rom)
	if err != nil {
		t.Fatalf("build ref: %v", err)
	}
	advanceFramesNES(t, ref, 60)
	saved, err := captureNESState(ref, "test-hash")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}

	// Continue the reference 30 more frames; remember the post-state.
	advanceFramesNES(t, ref, 30)
	wantPC := ref.cpu.PC
	wantFB := append([]byte(nil), ref.ppu.FrameBuffer()...)

	// Fresh bus from a clean rom build; run a different number of
	// frames (so it diverges from ref), then restore the saved
	// state. Both buses should now look identical.
	rom2, err := nes.ParseBytes(romBytes)
	if err != nil {
		t.Fatalf("parse rom 2: %v", err)
	}
	fresh, err := buildNES(rom2)
	if err != nil {
		t.Fatalf("build fresh: %v", err)
	}
	advanceFramesNES(t, fresh, 10) // intentionally different cadence
	if err := applyNESState(fresh, saved); err != nil {
		t.Fatalf("apply: %v", err)
	}
	advanceFramesNES(t, fresh, 30)

	if fresh.cpu.PC != wantPC {
		t.Errorf("post-load PC = $%04X; want $%04X", fresh.cpu.PC, wantPC)
	}
	gotFB := fresh.ppu.FrameBuffer()
	if len(gotFB) != len(wantFB) {
		t.Fatalf("framebuffer length mismatch: %d vs %d", len(gotFB), len(wantFB))
	}
	for i := range gotFB {
		if gotFB[i] != wantFB[i] {
			t.Fatalf("framebuffer divergence at byte %d: got %d want %d", i, gotFB[i], wantFB[i])
		}
	}
}

// encodeNESState round-trips through decodeNESState — gzip+gob path
// has to be lossless or every loaded slot is silently broken.
func TestSaveState_EncodeDecode_RoundTrip(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "vblank-bounce", "vblank-bounce.nes")
	romBytes, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read rom: %v", err)
	}
	rom, err := nes.ParseBytes(romBytes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	bus, err := buildNES(rom)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	advanceFramesNES(t, bus, 5)

	src, err := captureNESState(bus, "hashy-mchashface")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	data, err := encodeNESState(src)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := decodeNESState(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Magic != stateMagic || got.Version != stateVersion {
		t.Errorf("magic/version drift: %q v%d", got.Magic, got.Version)
	}
	if got.ROMHash != src.ROMHash {
		t.Errorf("rom hash drift")
	}
	if got.CPU.PC != src.CPU.PC || got.CPU.Cycles != src.CPU.Cycles {
		t.Errorf("CPU drift")
	}
	if got.PPU.FrameCount != src.PPU.FrameCount {
		t.Errorf("PPU frame count drift")
	}
}

// advanceFramesNES mirrors the game-loop step cadence headless.
func advanceFramesNES(t *testing.T, bus *nesBus, frames int) {
	t.Helper()
	target := bus.cpu.Cycles + uint64(frames)*cpuCyclesPerFrame
	for bus.cpu.Cycles < target && !bus.cpu.Halted {
		bus.cpu.Step()
	}
}
