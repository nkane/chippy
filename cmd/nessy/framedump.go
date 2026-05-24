//go:build nessy

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
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

// screenshotDir returns the user-facing screenshot directory (F12).
// Distinct from frameDumpDir so the bulky diagnostic dumps don't
// clutter the screenshots a user actually wants to keep.
func screenshotDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nessy", "screenshots")
}

// takeScreenshot writes the current framebuffer to
// $HOME/.nessy/screenshots/<rom>-<timestamp>.png. F12 binding.
// Best-effort; stderr-log on failure but never crash the game loop.
func (g *game) takeScreenshot() {
	dir := screenshotDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: mkdir screenshot:", err)
		return
	}
	stem := strings.TrimSuffix(g.titleBase, filepath.Ext(g.titleBase))
	stem = strings.TrimPrefix(stem, "nessy — ")
	if stem == "" {
		stem = "screenshot"
	}
	name := fmt.Sprintf("%s-%s.png", stem, time.Now().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	rgba := &image.RGBA{
		Pix:    g.bus.ppu.FrameBuffer(),
		Stride: 256 * 4,
		Rect:   image.Rect(0, 0, 256, 240),
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: screenshot create:", err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, rgba); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: screenshot encode:", err)
		return
	}
	fmt.Fprintln(os.Stderr, "nessy: screenshot saved:", path)
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
