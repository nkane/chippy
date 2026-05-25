//go:build nessy

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// state-counter increments a zero-page byte every NMI + writes
// it to $3F00. Each frame the framebuffer fills with the palette
// colour corresponding to (frame_cnt & 0x3F).
//
// This exercise:
//  1. boot, run 30 frames → frame_cnt = 30, framebuffer = palette[30].
//  2. capture save state.
//  3. run a fresh bus 80 frames → frame_cnt = 80, framebuffer = palette[80&0x3F].
//  4. apply the saved state.
//  5. assert the post-restore frame_cnt + framebuffer match the
//     reference at the same frame count.
//
// A bug in any subsystem persisted by save state (CPU regs, zero-
// page RAM, PPU palette, NMI latch, frame counter) breaks the
// check.
func TestDemo_StateCounter_SaveRoundTrip(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "state-counter", "state-counter.nes")
	romBytes, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read rom: %v", err)
	}
	rom, err := nes.ParseBytes(romBytes)
	if err != nil {
		t.Fatalf("parse rom: %v", err)
	}

	// Reference: 30 frames → capture.
	ref, err := buildNES(rom)
	if err != nil {
		t.Fatalf("build ref: %v", err)
	}
	advanceFramesNES(t, ref, 30)
	frameCntRef := ref.ram.Data[0x0000] // zero-page frame_cnt
	saved, err := captureNESState(ref, "state-counter-test")
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	refFB := append([]byte(nil), ref.ppu.FrameBuffer()...)

	// Fresh bus: advance 80 frames (intentionally divergent), then
	// restore + verify state matches reference.
	rom2, err := nes.ParseBytes(romBytes)
	if err != nil {
		t.Fatalf("parse rom 2: %v", err)
	}
	fresh, err := buildNES(rom2)
	if err != nil {
		t.Fatalf("build fresh: %v", err)
	}
	advanceFramesNES(t, fresh, 80)
	if err := applyNESState(fresh, saved); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := fresh.ram.Data[0x0000]; got != frameCntRef {
		t.Errorf("post-load frame_cnt = %d; want %d", got, frameCntRef)
	}
	gotFB := fresh.ppu.FrameBuffer()
	for i := range gotFB {
		if gotFB[i] != refFB[i] {
			t.Fatalf("framebuffer divergence at byte %d: got %d want %d", i, gotFB[i], refFB[i])
		}
	}
}
