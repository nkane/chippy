//go:build nestest

// nestest is the de-facto first test ROM for any NES emulator. It runs
// CPU-only checks at $C000 (skipping the cartridge-mode warmup at the
// $FFFC reset vector) and produces a documented sequence of PC /
// register / cycle transitions captured by Nintendulator as the
// reference log.
//
//	nestest.nes  https://www.qmtpro.com/~nes/misc/nestest.nes
//	nestest.log  https://www.qmtpro.com/~nes/misc/nestest.log
//
// The ROM is freely redistributable but not part of chippy's source
// tree; it is downloaded + cached on first run under
// $XDG_CACHE_HOME/chippy-tests/. SHA-256 is pinned (constants below);
// override with CHIPPY_NESTEST_BIN / CHIPPY_NESTEST_LOG to point at a
// local copy. Cache files failing the pin are silently re-downloaded.
//
// Run with:
//
//	go test -tags=nestest -timeout 5m -run TestNestest -v ./cmd/nessy/...
//
// Companion to internal/cpu's klaus_test.go (#30, #31, #32). Nestest
// validates the iNES → cart → MMIO → CPU integration end-to-end —
// klaus only exercises the bare CPU against a flat RAM.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/nes"
)

const (
	nestestROMURL   = "https://www.qmtpro.com/~nes/misc/nestest.nes"
	nestestLogURL   = "https://www.qmtpro.com/~nes/misc/nestest.log"
	nestestROMSHA   = "f67d55fd6b3cf0bad1cc85f1df0d739c65b53e79cecb7fea8f77ec0eadab0004"
	nestestLogSHA   = "627c8e180b1a924dfa705c5dc6958fad7ab75a62de556173caf880ccc1337540"
	nestestStartPC  = 0xC000
	nestestEndPC    = 0xC66E
	nestestMaxInstr = 30_000 // headless run is ~9k instructions; cap is generous
	nestestROMBytes = 24592  // 16-byte header + 16 KiB PRG + 8 KiB CHR
	nestestContextN = 5
)

// nestestExpected is the subset of register state captured from each
// golden-log line. PPU dot/scanline and cumulative CYC are present in
// the log but not diff'd here — the issue's contract is PC + A/X/Y/P/SP.
type nestestExpected struct {
	line int
	pc   uint16
	a    byte
	x    byte
	y    byte
	p    byte
	sp   byte
}

func TestNestest(t *testing.T) {
	romBytes, err := loadNestestFixture(t, "nestest.nes", nestestROMURL, "CHIPPY_NESTEST_BIN", nestestROMSHA)
	if err != nil {
		t.Skipf("nestest rom unavailable: %v", err)
	}
	if len(romBytes) != nestestROMBytes {
		t.Fatalf("nestest rom: want %d bytes, got %d", nestestROMBytes, len(romBytes))
	}
	logBytes, err := loadNestestFixture(t, "nestest.log", nestestLogURL, "CHIPPY_NESTEST_LOG", nestestLogSHA)
	if err != nil {
		t.Skipf("nestest log unavailable: %v", err)
	}
	expected, err := parseNestestLog(logBytes)
	if err != nil {
		t.Fatalf("parse nestest.log: %v", err)
	}
	if len(expected) == 0 {
		t.Fatalf("parsed nestest.log is empty")
	}

	rom, err := nes.ParseBytes(romBytes)
	if err != nil {
		t.Fatalf("parse iNES: %v", err)
	}
	bus, err := buildNES(rom)
	if err != nil {
		t.Fatalf("buildNES: %v", err)
	}
	// Headless entry: bypass the cartridge's $FFFC reset vector and
	// jump straight to $C000 per the nestest convention.
	bus.cpu.PC = nestestStartPC

	start := time.Now()
	for i := range nestestMaxInstr {
		if i >= len(expected) {
			t.Fatalf("ran past golden log (%d lines) without hitting end PC $%04X",
				len(expected), nestestEndPC)
		}
		exp := expected[i]
		got := nestestState(bus.cpu)
		got.line = exp.line // line is metadata, not state — copy through for diff equality
		if got != exp {
			t.Fatalf("nestest divergence at instruction %d (log line %d):\n%s",
				i+1, exp.line, nestestContext(expected, i, got))
		}
		if bus.cpu.PC == nestestEndPC {
			t.Logf("nestest PASSED %d instructions in %s",
				i+1, time.Since(start).Round(time.Millisecond))
			return
		}
		bus.cpu.Step()
	}
	t.Fatalf("nestest did not reach $%04X within %d instructions (last PC=$%04X)",
		nestestEndPC, nestestMaxInstr, bus.cpu.PC)
}

// nestestState snapshots the CPU's diffable fields. The line field is
// left zero — populated by the caller from the matching expected entry
// only when reporting divergence.
func nestestState(c *cpu.CPU) nestestExpected {
	return nestestExpected{
		pc: c.PC,
		a:  c.A,
		x:  c.X,
		y:  c.Y,
		p:  c.P,
		sp: c.SP,
	}
}

// parseNestestLog parses Nintendulator-style log lines:
//
//	C000  4C F5 C5  JMP $C5F5                       A:00 X:00 Y:00 P:24 SP:FD PPU:  0, 21 CYC:7
//
// Only the PC (cols 0-3) and the "A: X: Y: P: SP:" substrings matter
// for this test.
func parseNestestLog(data []byte) ([]nestestExpected, error) {
	var out []nestestExpected
	for ln, line := range strings.Split(string(data), "\n") {
		if len(line) < 4 {
			continue
		}
		pc, err := strconv.ParseUint(line[0:4], 16, 16)
		if err != nil {
			continue
		}
		a, err := substrHex(line, "A:")
		if err != nil {
			return nil, fmt.Errorf("line %d: A: missing/malformed: %w", ln+1, err)
		}
		x, err := substrHex(line, "X:")
		if err != nil {
			return nil, fmt.Errorf("line %d: X: missing/malformed: %w", ln+1, err)
		}
		y, err := substrHex(line, "Y:")
		if err != nil {
			return nil, fmt.Errorf("line %d: Y: missing/malformed: %w", ln+1, err)
		}
		p, err := substrHex(line, "P:")
		if err != nil {
			return nil, fmt.Errorf("line %d: P: missing/malformed: %w", ln+1, err)
		}
		sp, err := substrHex(line, "SP:")
		if err != nil {
			return nil, fmt.Errorf("line %d: SP: missing/malformed: %w", ln+1, err)
		}
		out = append(out, nestestExpected{
			line: ln + 1,
			pc:   uint16(pc),
			a:    a, x: x, y: y, p: p, sp: sp,
		})
	}
	return out, nil
}

// substrHex finds "key" in line, then parses the two hex digits
// following it. Returns an error if key is missing or the digits don't
// parse — both are signals the log is wrong-shape.
func substrHex(line, key string) (byte, error) {
	i := strings.Index(line, key)
	if i < 0 {
		return 0, fmt.Errorf("%q not found", key)
	}
	off := i + len(key)
	if off+2 > len(line) {
		return 0, fmt.Errorf("%q at end-of-line", key)
	}
	v, err := strconv.ParseUint(line[off:off+2], 16, 8)
	if err != nil {
		return 0, err
	}
	return byte(v), nil
}

// nestestContext renders a 5-line context window around the divergence:
// 2 lines before, the divergent line marked with `>>>`, 2 lines after.
func nestestContext(expected []nestestExpected, idx int, got nestestExpected) string {
	var b strings.Builder
	lo := idx - nestestContextN/2
	if lo < 0 {
		lo = 0
	}
	hi := lo + nestestContextN
	if hi > len(expected) {
		hi = len(expected)
	}
	fmt.Fprintf(&b, "%6s  %s  %s\n", "line", "expected", "got")
	for i := lo; i < hi; i++ {
		e := expected[i]
		marker := "    "
		if i == idx {
			marker = " >> "
		}
		fmt.Fprintf(&b, "%s%5d  PC=$%04X A=$%02X X=$%02X Y=$%02X P=$%02X SP=$%02X",
			marker, e.line, e.pc, e.a, e.x, e.y, e.p, e.sp)
		if i == idx {
			fmt.Fprintf(&b, "   <-- got PC=$%04X A=$%02X X=$%02X Y=$%02X P=$%02X SP=$%02X",
				got.pc, got.a, got.x, got.y, got.p, got.sp)
		}
		fmt.Fprintln(&b)
	}
	return b.String()
}

// loadNestestFixture resolves a fixture file in this order:
//
//  1. env var (a local path)
//  2. cache under $XDG_CACHE_HOME/chippy-tests/
//  3. HTTP download (cached on success)
//
// wantSHA pins the expected sha256. Cache files that don't match are
// silently re-downloaded.
func loadNestestFixture(t *testing.T, name, url, pathEnv, wantSHA string) ([]byte, error) {
	t.Helper()
	if p := os.Getenv(pathEnv); p != "" {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		if !verifyNestestSHA(data, wantSHA) {
			return nil, fmt.Errorf("%s: sha256 mismatch (got %s)", name, sha256Hex(data))
		}
		return data, nil
	}
	cachePath, err := nestestCachePath(name)
	if err != nil {
		return nil, err
	}
	if data, err := os.ReadFile(cachePath); err == nil {
		if verifyNestestSHA(data, wantSHA) {
			return data, nil
		}
		t.Logf("%s cache has wrong sha256 (got %s, want %s) — re-downloading",
			name, sha256Hex(data), wantSHA)
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil, err
	}
	t.Logf("downloading %s -> %s", name, cachePath)
	data, err := httpGetWithTimeout(url, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if !verifyNestestSHA(data, wantSHA) {
		return nil, fmt.Errorf("downloaded %s sha256 mismatch (got %s)", name, sha256Hex(data))
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Logf("warning: could not cache %s: %v", name, err)
	}
	return data, nil
}

func nestestCachePath(name string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "chippy-tests", name), nil
}

func httpGetWithTimeout(url string, timeout time.Duration) ([]byte, error) {
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

func verifyNestestSHA(data []byte, wantHex string) bool {
	sum := sha256.Sum256(data)
	return strings.EqualFold(hex.EncodeToString(sum[:]), wantHex)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
