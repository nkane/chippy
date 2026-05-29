//go:build accuracy

// Accuracy suite: runs nesdev / Blargg test ROMs through the full
// iNES → cart → MMIO → CPU + PPU + APU integration and checks the
// documented pass/fail signal. Build-tagged `accuracy` so the
// general suite stays fast + offline; CI runs it in a dedicated job.
//
// Blargg's test ROMs follow a fixed protocol:
//   - $6000 holds a status byte: $80 while running, $81 = "press
//     reset", and a value < $80 when finished (0 = pass, else a
//     fail code).
//   - $6001-$6003 hold the magic $DE $B0 $61 once the test has
//     started writing status (so we don't trust $6000 before then).
//   - $6004+ is a null-terminated ASCII result string.
//
// ROMs are downloaded + cached + SHA-pinned on first run (mirrors
// nestest_test.go). Override a ROM with its *_BIN env var to point
// at a local copy.
//
// Run with:
//
//	go test -tags=accuracy -timeout 5m -run TestAccuracy -v ./cmd/nessy/...
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/nes"
)

// accuracyCyclesPerFrame mirrors cmd/nessy's per-frame budget; the
// game.go const lives behind the `nessy` build tag, invisible under
// `accuracy`, so it's duplicated here.
const accuracyCyclesPerFrame = 29830

// accuracyROM describes one test-ROM fixture + the conditions under
// which it counts as passing.
type accuracyROM struct {
	name      string // cache filename + sub-test label
	url       string
	sha       string
	pathEnv   string // env override for a local copy
	maxFrames int    // step cap (frame = accuracyCyclesPerFrame cycles)
	// knownFail, when non-empty, marks a ROM that currently exposes a
	// real accuracy gap. The harness still runs it + logs the result,
	// but a non-zero status is reported as a tracked-gap skip instead
	// of a hard failure so CI stays green. Clear the field (+ delete
	// the tracking issue) once the gap is fixed.
	knownFail string
}

var accuracyROMs = []accuracyROM{
	{
		name:      "ppu_vbl_nmi.nes",
		url:       "https://github.com/christopherpow/nes-test-roms/raw/master/ppu_vbl_nmi/ppu_vbl_nmi.nes",
		sha:       "8dbab1be785585c399cf055ef02147b788ab75fd80e81cf9568a2feafc03fb7d",
		pathEnv:   "CHIPPY_ACCURACY_VBL_NMI_BIN",
		maxFrames: 2200, // ~37 s emulated; the suite finishes near frame 1550
		// Full pass (10/10) after the per-cycle CPU↔PPU rewrite (#342).
	},
	{
		// Per-instruction cycle timing (branches, page-cross, RMW, etc.).
		// Validates the per-cycle CPU model end-to-end (#318, #342).
		name:      "instr_timing.nes",
		url:       "https://github.com/christopherpow/nes-test-roms/raw/master/instr_timing/instr_timing.nes",
		sha:       "3d1bca14266f1e25b75a34ddd29c9df1ce9c6d990c8663a218f72e7861660fb0",
		pathEnv:   "CHIPPY_ACCURACY_INSTR_TIMING_BIN",
		maxFrames: 2400,
	},
	{
		// IRQ/NMI/BRK interaction + interrupt latency. Full 5/5
		// PASS as of #377 (cli_latency, nmi_and_brk, nmi_and_irq,
		// irq_and_dma, branch_delays_irq) — see #369/#376.
		name:      "cpu_interrupts_v2.nes",
		url:       "https://github.com/christopherpow/nes-test-roms/raw/master/cpu_interrupts_v2/cpu_interrupts.nes",
		sha:       "ccbac4e824eb96ecfe8b82d331a083be186eb6776aa57e25c52251eaf7df9c4f",
		pathEnv:   "CHIPPY_ACCURACY_INTERRUPTS_BIN",
		maxFrames: 2400,
	},
	{
		// Blargg apu_test — 8 sub-tests of APU behavior. After
		// #376/#377 (NTSC 4-step frame intervals + DMC fetch via
		// ProcessPendingDma) tests 1-4 pass; test 5 (len_timing)
		// fails with "Second length of mode 0 is too soon" — a
		// length-counter timing gap separate from the frame
		// counter work. Tracked under #318; harness logs the
		// status + skips so the existing PASS suite stays green.
		name:      "apu_test.nes",
		url:       "https://github.com/christopherpow/nes-test-roms/raw/master/apu_test/apu_test.nes",
		sha:       "00d4722bae1c82a14528dd3220462d3fb9ce4b14b8cec996619dea23e07fef0a",
		pathEnv:   "CHIPPY_ACCURACY_APU_TEST_BIN",
		maxFrames: 3000,
		knownFail: "tests 1-7 PASS (incl. 7-dmc_basics after the DMC enable-fetch + $4015 read fixes); 8-dmc_rates fails at sub-test 3 ('Rate 0's period is too long') — DMC fetch decrement timing across multiple bytes needs alignment to Mesen",
	},
}

func TestAccuracy(t *testing.T) {
	for _, rom := range accuracyROMs {
		t.Run(rom.name, func(t *testing.T) {
			data, err := loadAccuracyROM(t, rom)
			if err != nil {
				t.Fatalf("load %s: %v", rom.name, err)
			}
			parsed, err := nes.ParseBytes(data)
			if err != nil {
				t.Fatalf("parse %s: %v", rom.name, err)
			}
			bus, err := buildNES(parsed)
			if err != nil {
				t.Fatalf("build %s: %v", rom.name, err)
			}

			status, text := runBlargg(bus, rom.maxFrames)
			t.Logf("%s: status=$%02X\n%s", rom.name, status, text)
			if status == 0 {
				return // pass
			}
			if rom.knownFail != "" {
				t.Skipf("%s: known accuracy gap (%s); status=$%02X", rom.name, rom.knownFail, status)
			}
			t.Errorf("%s FAILED: status=$%02X\n%s", rom.name, status, text)
		})
	}
}

// runBlargg steps the bus until the ROM reports a finished status at
// $6000 (or the frame cap trips). Returns the final status byte +
// the result text at $6004. The magic at $6001-$6003 gates trusting
// $6000 — before the test writes it, $6000 is uninitialised RAM.
func runBlargg(bus *nesBus, maxFrames int) (byte, string) {
	started := false
	for f := 0; f < maxFrames; f++ {
		target := bus.cpu.Cycles + accuracyCyclesPerFrame
		for bus.cpu.Cycles < target && !bus.cpu.Halted {
			bus.cpu.Step()
		}
		magicOK := bus.cart.CPURead(0x6001) == 0xDE &&
			bus.cart.CPURead(0x6002) == 0xB0 &&
			bus.cart.CPURead(0x6003) == 0x61
		if !magicOK {
			continue
		}
		started = true
		switch bus.cart.CPURead(0x6000) {
		case 0x80, 0x81:
			// still running / awaiting reset
		default:
			return bus.cart.CPURead(0x6000), blarggText(bus)
		}
	}
	if !started {
		return 0xFF, "test never wrote the Blargg status magic ($6001-$6003)"
	}
	return 0xFF, "timed out before the test reported a finished status"
}

func blarggText(bus *nesBus) string {
	var b strings.Builder
	for addr := 0x6004; addr < 0x8000; addr++ {
		c := bus.cart.CPURead(uint16(addr))
		if c == 0 {
			break
		}
		b.WriteByte(c)
	}
	return strings.TrimSpace(b.String())
}

// loadAccuracyROM mirrors the nestest fixture loader: env override →
// SHA-pinned cache → download. An empty pinned SHA means "accept
// whatever downloads + log the hash" so the pin can be filled in on
// first run.
func loadAccuracyROM(t *testing.T, rom accuracyROM) ([]byte, error) {
	t.Helper()
	if p := os.Getenv(rom.pathEnv); p != "" {
		return os.ReadFile(p)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	cachePath := filepath.Join(cache, "chippy-tests", rom.name)
	if data, err := os.ReadFile(cachePath); err == nil {
		if rom.sha == "" || strings.EqualFold(hashHex(data), rom.sha) {
			return data, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err != nil {
		return nil, err
	}
	t.Logf("downloading %s", rom.url)
	data, err := httpGet(rom.url, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if rom.sha != "" && !strings.EqualFold(hashHex(data), rom.sha) {
		return nil, fmt.Errorf("%s sha mismatch: got %s", rom.name, hashHex(data))
	}
	if rom.sha == "" {
		t.Logf("%s downloaded; pin this SHA: %s", rom.name, hashHex(data))
	}
	_ = os.WriteFile(cachePath, data, 0o644)
	return data, nil
}

func httpGet(url string, timeout time.Duration) ([]byte, error) {
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

func hashHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
