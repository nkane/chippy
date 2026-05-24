//go:build nessy

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

// frameDumpDir returns the directory frame PNGs land in. Empty when
// $HOME isn't resolvable — dump silently disabled.
func frameDumpDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nessy", "dumps")
}

// dumpFrame writes the current PPU framebuffer (256 × 240 RGBA) to
// $HOME/.nessy/dumps/F<frame>.png. Used by the -frame-dump-every
// flag for "scrub the recording after a play session" diagnostics.
// Each frame is its own file; user prunes after analysis.
// Best-effort — errors logged but not fatal.
func (g *game) dumpFrame() {
	dir := frameDumpDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: mkdir frame-dump:", err)
		return
	}
	rgba := &image.RGBA{
		Pix:    g.bus.ppu.FrameBuffer(),
		Stride: 256 * 4,
		Rect:   image.Rect(0, 0, 256, 240),
	}
	path := filepath.Join(dir, fmt.Sprintf("F%07d.png", g.frameNum))
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: frame-dump create:", err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, rgba); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: frame-dump encode:", err)
		return
	}
}
