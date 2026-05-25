//go:build klaus

// 65C02 companion to klaus_test.go. Klaus Dormann's extended opcodes test
// exercises CMOS-only instructions (STZ, PHX/PHY/PLX/PLY, BRA, BBR0-7,
// BBS0-7, TRB, TSB, INC A, plus the variant ADC/SBC + JMP (abs,x)
// changes). Same harness conventions as the NMOS test: 64K ROM image
// placed at $0000, code entry at $0400, success on self-jump at the
// fixed pass trap, any other PC self-jump means a subtest failed.
//
// The ROM is GPL-3.0 so it's downloaded + sha256-verified on demand,
// not vendored.

package cpu

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	klausCMOSURL    = "https://github.com/Klaus2m5/6502_65C02_functional_tests/raw/master/bin_files/65C02_extended_opcodes_test.bin"
	klausCMOSSHA256 = "10a2a07fa240666fa610c46accebe8d42b1000feef3aae619da15a8d152869b2"
	klausCMOSSize   = 65536
	klausCMOSStart  = 0x0400
	klausCMOSPass   = 0x24F1
)

func TestKlausCMOSFunctionalTest(t *testing.T) {
	bin, err := loadKlausCMOSBinary(t)
	if err != nil {
		t.Skipf("klaus cmos rom unavailable (set CHIPPY_KLAUS_CMOS_BIN to a local copy): %v", err)
	}
	if len(bin) != klausCMOSSize {
		t.Fatalf("klaus cmos rom: want %d bytes, got %d", klausCMOSSize, len(bin))
	}

	ram := NewRAM()
	ram.Load(0x0000, bin)

	c := NewVariant(ram, VariantCMOS65C02)
	c.Reset()
	c.PC = klausCMOSStart // bypass reset trap per suite convention

	start := time.Now()
	for i := 0; i < klausMaxInstr; i++ {
		pc := c.PC
		c.Step()
		if c.PC == pc {
			if pc == klausCMOSPass {
				t.Logf("klaus 65c02 functional test PASSED in %d instructions, %s",
					i+1, time.Since(start).Round(time.Millisecond))
				return
			}
			t.Fatalf("klaus 65c02 functional test FAILED: trap at PC=$%04X "+
				"after %d instructions  A=%02X X=%02X Y=%02X SP=%02X P=%02X",
				pc, i+1, c.A, c.X, c.Y, c.SP, c.P)
		}
	}
	t.Fatalf("klaus 65c02 functional test did not converge within %d instructions "+
		"(last PC=$%04X)", klausMaxInstr, c.PC)
}

// loadKlausCMOSBinary mirrors loadKlausBinary's resolution order. Cache
// file uses a different basename so the NMOS + CMOS caches coexist.
func loadKlausCMOSBinary(t *testing.T) ([]byte, error) {
	t.Helper()
	if p := os.Getenv("CHIPPY_KLAUS_CMOS_BIN"); p != "" {
		return os.ReadFile(p)
	}
	cachePath, err := klausCMOSCachePath()
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(cachePath); err == nil {
		if verifySHA256(data, klausCMOSSHA256) {
			return data, nil
		}
		t.Logf("klaus cmos cache file %s has wrong sha256, re-downloading", cachePath)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil, err
	}
	t.Logf("downloading klaus 65c02 test rom -> %s", cachePath)
	data, err := httpDownload(klausCMOSURL, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !verifySHA256(data, klausCMOSSHA256) {
		return nil, fmt.Errorf("downloaded klaus cmos rom sha256 mismatch")
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Logf("warning: could not cache klaus cmos rom: %v", err)
	}
	return data, nil
}

func klausCMOSCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chippy-tests", "klaus_65c02_extended_opcodes_test.bin"), nil
}
