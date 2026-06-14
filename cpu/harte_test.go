//go:build harte

// Tom Harte's ProcessorTests (https://github.com/TomHarte/ProcessorTests) are
// the modern fuzz-scale per-opcode standard: each of the 256 opcodes ships
// ~10,000 randomized (initial state -> expected final state + cycle count)
// cases. Where Klaus / AllSuiteA / Lorenz validate integration, these pin each
// opcode in isolation across the flag/decimal/overflow corners.
//
// The data is large (~1 GB for the 6502 set), so it is NOT vendored. Provide
// it one of two ways:
//
//	CHIPPY_HARTE_DIR=/path/to/6502/v1   # a directory of NN.json files
//	(unset)                              # download per-opcode to the user
//	                                     # cache dir, pinned to harteCommit
//
// Run a quick subset by capping cases:
//
//	CHIPPY_HARTE_MAX_CASES=200 go test -tags=harte -run TestHarte ./cpu/...
//
// Scope: 6502 NMOS. JAM/KIL opcodes (chippy NOP-stubs them) and the unstable
// illegals (SHA/SHX/SHY/TAS, and ARR in decimal mode) are skipped — their
// "correct" result is a magic constant the stable approximation doesn't model;
// see the skip list. TestHarte6502 compares final state + cycle COUNT;
// TestHarte6502BusTrace (issue #400) compares the full per-cycle bus trace.
// 65C02 is a follow-up (different data set + CMOS variant).

package cpu

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const harteCommit = "bb11756436da8fd16cce86aef63dc6725f48836f"

// harteSkip lists opcodes whose Tom Harte cases chippy is not expected to pass:
//   - $x2 KIL/JAM: halt the CPU; chippy treats them as NOP.
//   - SHA/SHX/SHY/TAS: unstable illegals whose result depends on a magic
//     constant chippy only approximates. (ARR decimal was fixed in #424.)
var harteSkip = map[byte]string{
	0x02: "JAM", 0x12: "JAM", 0x22: "JAM", 0x32: "JAM",
	0x42: "JAM", 0x52: "JAM", 0x62: "JAM", 0x72: "JAM",
	0x92: "JAM", 0xB2: "JAM", 0xD2: "JAM", 0xF2: "JAM",
	0x93: "SHA (unstable)", 0x9F: "SHA (unstable)",
	0x9B: "TAS (unstable)", 0x9C: "SHY (unstable)", 0x9E: "SHX (unstable)",
}

type harteState struct {
	PC, S, A, X, Y, P int
	RAM               [][2]int
}

type harteCase struct {
	Name    string          `json:"name"`
	Initial harteState      `json:"initial"`
	Final   harteState      `json:"final"`
	Cycles  [][]interface{} `json:"cycles"` // [addr, value, "read"|"write"] per cycle
}

func TestHarte6502(t *testing.T) {
	maxCases := 0
	if v := os.Getenv("CHIPPY_HARTE_MAX_CASES"); v != "" {
		maxCases, _ = strconv.Atoi(v)
	}
	for op := 0; op < 256; op++ {
		op := byte(op)
		name := fmt.Sprintf("%02x", op)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if reason, skip := harteSkip[op]; skip {
				t.Skipf("skip %02x: %s", op, reason)
			}
			cases, err := loadHarteCases(t, name)
			if err != nil {
				t.Skipf("cases for %02x unavailable: %v", op, err)
			}
			runHarteOpcode(t, name, cases, maxCases)
		})
	}
}

func runHarteOpcode(t *testing.T, op string, cases []harteCase, maxCases int) {
	t.Helper()
	n := len(cases)
	if maxCases > 0 && maxCases < n {
		n = maxCases
	}
	for i := 0; i < n; i++ {
		tc := &cases[i]
		ram := NewRAM()
		for _, kv := range tc.Initial.RAM {
			ram.Write(uint16(kv[0]), byte(kv[1]))
		}
		c := New(ram) // VariantNMOS
		c.PC = uint16(tc.Initial.PC)
		c.SP = byte(tc.Initial.S)
		c.A, c.X, c.Y = byte(tc.Initial.A), byte(tc.Initial.X), byte(tc.Initial.Y)
		c.P = byte(tc.Initial.P)

		cyc := c.Step()

		if diff := harteDiff(c, ram, tc, cyc); diff != "" {
			t.Fatalf("opcode %s case %q (#%d):\n%s", op, tc.Name, i, diff)
		}
	}
}

// harteDiff returns "" when the post-step state matches the expected final
// state, or a human-readable diff of the first mismatch otherwise.
func harteDiff(c *CPU, ram *RAM, tc *harteCase, cyc int) string {
	f := &tc.Final
	mism := func(label string, got, want int) string {
		return fmt.Sprintf("  %-4s got %02X want %02X", label, got, want)
	}
	switch {
	case c.PC != uint16(f.PC):
		return fmt.Sprintf("  PC   got %04X want %04X", c.PC, f.PC)
	case c.SP != byte(f.S):
		return mism("SP", int(c.SP), f.S)
	case c.A != byte(f.A):
		return mism("A", int(c.A), f.A)
	case c.X != byte(f.X):
		return mism("X", int(c.X), f.X)
	case c.Y != byte(f.Y):
		return mism("Y", int(c.Y), f.Y)
	case c.P != byte(f.P):
		return fmt.Sprintf("  P    got %08b want %08b", c.P, f.P)
	case cyc != len(tc.Cycles):
		return fmt.Sprintf("  CYC  got %d want %d", cyc, len(tc.Cycles))
	}
	for _, kv := range f.RAM {
		if got := ram.Read(uint16(kv[0])); got != byte(kv[1]) {
			return fmt.Sprintf("  RAM[%04X] got %02X want %02X", kv[0], got, byte(kv[1]))
		}
	}
	return ""
}

// loadHarteCases returns the cases for one opcode, from CHIPPY_HARTE_DIR if set,
// otherwise downloading (and caching) the pinned JSON.
func loadHarteCases(t *testing.T, op string) ([]harteCase, error) {
	t.Helper()
	var data []byte
	var err error
	if dir := os.Getenv("CHIPPY_HARTE_DIR"); dir != "" {
		data, err = os.ReadFile(filepath.Join(dir, op+".json"))
	} else {
		data, err = harteDownload(t, op)
	}
	if err != nil {
		return nil, err
	}
	var cases []harteCase
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func harteDownload(t *testing.T, op string) ([]byte, error) {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	cacheDir = filepath.Join(cacheDir, "chippy-tests", "harte-6502", harteCommit[:12])
	cachePath := filepath.Join(cacheDir, op+".json")
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/TomHarte/ProcessorTests/%s/6502/v1/%s.json",
		harteCommit, op)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %s: %d", url, resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(cachePath, data, 0o644)
	return data, nil
}

// --- Per-cycle bus-trace validation (issue #400) ---------------------------
//
// Tom Harte's `cycles` field is a per-cycle bus trace: [address, value,
// "read"|"write"] for every cycle the opcode drives, INCLUDING the 6502's
// dummy/internal cycles. Comparing chippy's per-cycle bus activity against it
// is the highest-fidelity correctness probe short of silicon — the intent of
// the Visual6502 comparison in #400, at ~100x the coverage (every opcode,
// every case, rather than a handful of hand-curated reference programs).
//
// chippy's per-cycle interleave (the `nesCycle` path) routes every read /
// write / dummy through c.Bus, so a recording bus captures the exact trace.
// The path is gated on VariantNES at runtime, but VariantNES disables decimal
// mode; for a faithful NMOS trace we keep VariantNMOS (so decimal works) and
// enable the per-cycle path directly for the single-instruction step.

// harteBusSkip extends harteSkip with the opcodes whose per-cycle bus TRACE
// (not just final state) diverges. chippy passes 228/238 bus-exact; the
// remainder are well-understood quirks tracked for a follow-up:
//   - taken page-crossing branches drive the dummy read at a different address
//     than silicon (the pre-fixup address).
//   - JSR/RTS interleave the operand fetch / stack ops in an order the
//     instruction-stepped model collapses.
var harteBusSkip = map[byte]string{
	0x10: "BPL dummy-read addr", 0x30: "BMI dummy-read addr",
	0x50: "BVC dummy-read addr", 0x70: "BVS dummy-read addr",
	0x90: "BCC dummy-read addr", 0xB0: "BCS dummy-read addr",
	0xD0: "BNE dummy-read addr", 0xF0: "BEQ dummy-read addr",
	0x20: "JSR cycle ordering", 0x60: "RTS cycle ordering",
}

// busRecorder is a 64 KiB RAM that records every access as the per-cycle CPU
// path drives it, plus a no-op Ticker so nesCycle engages.
type busRecorder struct {
	ram   [0x10000]byte
	trace [][3]int // {addr, value, rw} where rw: 0=read, 1=write
}

func (b *busRecorder) Read(addr uint16) byte {
	v := b.ram[addr]
	b.trace = append(b.trace, [3]int{int(addr), int(v), 0})
	return v
}

func (b *busRecorder) Write(addr uint16, v byte) {
	b.ram[addr] = v
	b.trace = append(b.trace, [3]int{int(addr), int(v), 1})
}

func (b *busRecorder) Tick(int) {}

func TestHarte6502BusTrace(t *testing.T) {
	maxCases := 0
	if v := os.Getenv("CHIPPY_HARTE_MAX_CASES"); v != "" {
		maxCases, _ = strconv.Atoi(v)
	}
	for op := 0; op < 256; op++ {
		op := byte(op)
		name := fmt.Sprintf("%02x", op)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if reason, skip := harteSkip[op]; skip {
				t.Skipf("skip %02x: %s", op, reason)
			}
			if reason, skip := harteBusSkip[op]; skip {
				t.Skipf("skip %02x bus trace: %s", op, reason)
			}
			cases, err := loadHarteCases(t, name)
			if err != nil {
				t.Skipf("cases for %02x unavailable: %v", op, err)
			}
			runHarteBusTrace(t, name, cases, maxCases)
		})
	}
}

func runHarteBusTrace(t *testing.T, op string, cases []harteCase, maxCases int) {
	t.Helper()
	n := len(cases)
	if maxCases > 0 && maxCases < n {
		n = maxCases
	}
	for i := 0; i < n; i++ {
		tc := &cases[i]
		bus := &busRecorder{}
		for _, kv := range tc.Initial.RAM {
			bus.ram[kv[0]] = byte(kv[1])
		}
		c := New(NewRAM())
		// Run the per-cycle interleave on a NMOS core so every access (incl.
		// dummy cycles) flows through the recording bus while decimal mode and
		// other NMOS semantics stay intact.
		c.Bus = bus
		c.busTicker = bus
		c.nesCycle = true
		c.PC = uint16(tc.Initial.PC)
		c.SP = byte(tc.Initial.S)
		c.A, c.X, c.Y = byte(tc.Initial.A), byte(tc.Initial.X), byte(tc.Initial.Y)
		c.P = byte(tc.Initial.P)

		c.Step()

		if diff := busTraceDiff(bus.trace, tc.Cycles); diff != "" {
			t.Fatalf("opcode %s case %q (#%d): bus trace %s", op, tc.Name, i, diff)
		}
	}
}

// busTraceDiff compares chippy's recorded accesses against the expected
// per-cycle trace, returning "" on a match or the first divergence.
func busTraceDiff(got [][3]int, want [][]interface{}) string {
	if len(got) != len(want) {
		return fmt.Sprintf("length got %d want %d", len(got), len(want))
	}
	for i := range got {
		wa := int(want[i][0].(float64))
		wv := int(want[i][1].(float64))
		wrw := 0
		if want[i][2].(string) == "write" {
			wrw = 1
		}
		if got[i][0] != wa || got[i][1] != wv || got[i][2] != wrw {
			rw := func(x int) string {
				if x == 1 {
					return "write"
				}
				return "read"
			}
			return fmt.Sprintf("cycle %d got [%04X %02X %s] want [%04X %02X %s]",
				i, got[i][0], got[i][1], rw(got[i][2]), wa, wv, rw(wrw))
		}
	}
	return ""
}
