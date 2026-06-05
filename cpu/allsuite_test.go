//go:build klaus

// AllSuiteA is Frank Kingswood's compact NMOS-6502 smoke ROM: a single
// ~1.5 KB program that exercises every opcode under common addressing modes
// and reports the outcome in one byte. It's a cheap complement to the Klaus
// functional suite (klaus_test.go) — it runs in a few hundred instructions
// and catches gross opcode/addressing regressions fast.
//
// The ROM (GPL-era community test code) is NOT vendored; it's downloaded on
// first run and cached alongside the Klaus binary, sharing httpDownload /
// verifySHA256 from klaus_test.go. Both live under the `klaus` build tag and
// the same CI job (`-run 'TestKlaus|TestAllSuite'`).
//
// Layout (verified against AllSuiteA.asm @ pmonta/FPGA-netlist-tools):
//
//	*= $4000          load + entry point
//	$0210 = $FF       success; any other value is the failing test number
//	theend: JMP *     both pass and fail end in a self-loop at $45C0, so the
//	                  PC self-loop alone can't distinguish them — the result
//	                  byte at $0210 is authoritative.

package cpu

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	allSuiteURL    = "https://raw.githubusercontent.com/pmonta/FPGA-netlist-tools/master/6502-test-code/AllSuiteA.bin"
	allSuiteSHA256 = "4801c945bd68ba1bff20dbce45d11889d945c28058bb92fbf52ed59e7aed964b"
	allSuiteSize   = 1475
	allSuiteLoad   = 0x4000
	allSuiteResult = 0x0210
	allSuitePass   = 0xFF
	// The ROM converges in well under a thousand instructions; this bound is
	// pure runaway protection.
	allSuiteMaxInstr = 1_000_000
)

func TestAllSuiteA(t *testing.T) {
	bin, err := loadAllSuiteBinary(t)
	if err != nil {
		t.Skipf("AllSuiteA rom unavailable (set CHIPPY_ALLSUITE_BIN to a local copy): %v", err)
	}
	if len(bin) != allSuiteSize {
		t.Fatalf("AllSuiteA rom: want %d bytes, got %d", allSuiteSize, len(bin))
	}

	ram := NewRAM()
	ram.Load(allSuiteLoad, bin)

	c := New(ram) // VariantNMOS
	c.Reset()
	c.PC = allSuiteLoad // ROM has no reset vector; enter at the load address

	start := time.Now()
	for i := 0; i < allSuiteMaxInstr; i++ {
		pc := c.PC
		c.Step()
		if c.PC != pc {
			continue
		}
		// Reached the terminal self-loop. The result byte decides pass/fail.
		got := ram.Read(allSuiteResult)
		if got == allSuitePass {
			t.Logf("AllSuiteA PASSED in %d instructions, %s",
				i+1, time.Since(start).Round(time.Millisecond))
			return
		}
		t.Fatalf("AllSuiteA FAILED: $%04X = $%02X (failing test number; $FF = pass) "+
			"trap PC=$%04X after %d instructions  A=%02X X=%02X Y=%02X SP=%02X P=%02X",
			allSuiteResult, got, pc, i+1, c.A, c.X, c.Y, c.SP, c.P)
	}
	t.Fatalf("AllSuiteA did not converge within %d instructions (last PC=$%04X, $%04X=$%02X)",
		allSuiteMaxInstr, c.PC, allSuiteResult, ram.Read(allSuiteResult))
}

// loadAllSuiteBinary mirrors loadKlausBinary's resolution order:
//  1. CHIPPY_ALLSUITE_BIN env var (path to a local copy)
//  2. cached file under the user cache dir (verified by sha256)
//  3. HTTP download (skipped if no network)
func loadAllSuiteBinary(t *testing.T) ([]byte, error) {
	t.Helper()
	if p := os.Getenv("CHIPPY_ALLSUITE_BIN"); p != "" {
		return os.ReadFile(p)
	}
	cachePath, err := allSuiteCachePath()
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(cachePath); err == nil {
		if verifySHA256(data, allSuiteSHA256) {
			return data, nil
		}
		t.Logf("AllSuiteA cache file %s has wrong sha256, re-downloading", cachePath)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil, err
	}
	t.Logf("downloading AllSuiteA test rom -> %s", cachePath)
	data, err := httpDownload(allSuiteURL, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !verifySHA256(data, allSuiteSHA256) {
		return nil, fmt.Errorf("downloaded AllSuiteA rom sha256 mismatch (got %s)",
			hex.EncodeToString(sha256SumOf(data)))
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Logf("warning: could not cache AllSuiteA rom: %v", err)
	}
	return data, nil
}

func allSuiteCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chippy-tests", "AllSuiteA.bin"), nil
}
