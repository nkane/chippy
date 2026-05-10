//go:build klaus

// Package cpu's klaus_test runs Klaus Dormann's 6502 functional test suite
// (https://github.com/Klaus2m5/6502_65C02_functional_tests) against the CPU
// core. The test ROM is GPL-3.0 so it is NOT vendored into this repository;
// instead it is downloaded on first run and cached under
// $XDG_CACHE_HOME/chippy-tests/ (or ~/.cache/chippy-tests/).
//
// Run with:
//
//	go test -tags=klaus -timeout 5m -run TestKlaus ./internal/cpu/...
//
// Trap PCs (verified against bin_files/6502_functional_test.lst @ master):
//
//	$3469  success  — JMP *  reached only if every subtest passed
//	other  failure  — any other PC=PC self-loop is a failing subtest trap
//	                  whose address points into the failing test code
//
// The test ROM ignores the reset vector (it points to $37A3 res_trap, a
// deliberate trap). Per the suite's documented harness convention we set
// PC = $0400 (the `start` label) directly after Reset().

package cpu

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	klausURL    = "https://github.com/Klaus2m5/6502_65C02_functional_tests/raw/master/bin_files/6502_functional_test.bin"
	klausSHA256 = "fa12bfc761e6f9057e4cc01a665a7b800ff01ae91f598af1e39a1201d01953fd"
	klausSize   = 65536
	klausStart  = 0x0400
	klausPass   = 0x3469
	// Soft cap. The full suite runs in ~30M instructions on a real harness;
	// give a generous bound so a slow CPU still finishes well under 5 min.
	klausMaxInstr = 100_000_000
)

func TestKlausFunctionalTest(t *testing.T) {
	bin, err := loadKlausBinary(t)
	if err != nil {
		t.Skipf("klaus rom unavailable (set CHIPPY_KLAUS_BIN to a local copy): %v", err)
	}
	if len(bin) != klausSize {
		t.Fatalf("klaus rom: want %d bytes, got %d", klausSize, len(bin))
	}

	ram := NewRAM()
	ram.Load(0x0000, bin)

	c := New(ram)
	c.Reset()
	c.PC = klausStart // bypass res_trap reset vector per suite convention

	start := time.Now()
	prevPC := uint16(0xFFFF)
	for i := 0; i < klausMaxInstr; i++ {
		pc := c.PC
		c.Step()
		if c.PC == pc {
			// Self-loop = trap. Either success ($3469) or a failing subtest.
			if pc == klausPass {
				t.Logf("klaus functional test PASSED in %d instructions, %s",
					i+1, time.Since(start).Round(time.Millisecond))
				return
			}
			t.Fatalf("klaus functional test FAILED: trap at PC=$%04X "+
				"after %d instructions  A=%02X X=%02X Y=%02X SP=%02X P=%02X",
				pc, i+1, c.A, c.X, c.Y, c.SP, c.P)
		}
		prevPC = pc
	}
	_ = prevPC
	t.Fatalf("klaus functional test did not converge within %d instructions "+
		"(last PC=$%04X)", klausMaxInstr, c.PC)
}

// loadKlausBinary returns the test ROM bytes, fetching + caching on demand.
// Resolution order:
//  1. CHIPPY_KLAUS_BIN env var (path to a local copy)
//  2. Cached file under user cache dir (verified by sha256)
//  3. HTTP download (skipped if no network)
func loadKlausBinary(t *testing.T) ([]byte, error) {
	t.Helper()
	if p := os.Getenv("CHIPPY_KLAUS_BIN"); p != "" {
		return os.ReadFile(p)
	}
	cachePath, err := klausCachePath()
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(cachePath); err == nil {
		if verifySHA256(data, klausSHA256) {
			return data, nil
		}
		t.Logf("klaus cache file %s has wrong sha256, re-downloading", cachePath)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil, err
	}
	t.Logf("downloading klaus test rom -> %s", cachePath)
	data, err := httpDownload(klausURL, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !verifySHA256(data, klausSHA256) {
		return nil, fmt.Errorf("downloaded klaus rom sha256 mismatch (got %s)",
			hex.EncodeToString(sha256SumOf(data)))
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Logf("warning: could not cache klaus rom: %v", err)
	}
	return data, nil
}

func klausCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chippy-tests", "klaus_6502_functional_test.bin"), nil
}

func httpDownload(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func sha256SumOf(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func verifySHA256(data []byte, want string) bool {
	return hex.EncodeToString(sha256SumOf(data)) == want
}
