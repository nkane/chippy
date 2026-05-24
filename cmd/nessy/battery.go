//go:build nessy

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nkane/chippy/internal/nes/cart"
)

// batteryDir is the resolved directory used for .sav files. Empty
// when the host has no usable HOME — battery persistence
// silently disabled.
func batteryDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nessy", "sav")
}

// savPath returns the .sav file path for a ROM, keyed by the
// SHA-256 of the ROM bytes so renaming the ROM doesn't orphan the
// save.
func savPath(romBytes []byte) string {
	dir := batteryDir()
	if dir == "" {
		return ""
	}
	sum := sha256.Sum256(romBytes)
	return filepath.Join(dir, hex.EncodeToString(sum[:])+".sav")
}

// loadBattery reads a sibling .sav for the cart's PRG-RAM if the
// cart is battery-backed + the file exists + sizes match. Silent
// no-op otherwise. Errors logged to stderr but never fatal —
// users who can't load a save can still play.
func loadBattery(c cart.Cartridge, romBytes []byte) {
	if !c.BatteryBacked() {
		return
	}
	ram := c.PRGRAM()
	if ram == nil {
		return
	}
	path := savPath(romBytes)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, "nessy: load battery .sav:", err)
		}
		return
	}
	if len(data) != len(ram) {
		fmt.Fprintf(os.Stderr, "nessy: .sav size mismatch (%d vs %d) — ignoring\n", len(data), len(ram))
		return
	}
	copy(ram, data)
	fmt.Fprintln(os.Stderr, "nessy: battery .sav loaded from", path)
}

// saveBattery writes the cart's PRG-RAM to the sibling .sav file.
// Best-effort — errors logged, never fatal.
func saveBattery(c cart.Cartridge, romBytes []byte) {
	if !c.BatteryBacked() {
		return
	}
	ram := c.PRGRAM()
	if ram == nil {
		return
	}
	path := savPath(romBytes)
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: mkdir .sav:", err)
		return
	}
	if err := os.WriteFile(path, ram, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: write battery .sav:", err)
		return
	}
	fmt.Fprintln(os.Stderr, "nessy: battery .sav written to", path)
}
