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
// Scope: 6502 NMOS + WDC 65C02. JAM/KIL opcodes (chippy NOP-stubs them) and the
// unstable illegals (SHA/SHX/SHY/TAS, and ARR in decimal mode) are skipped —
// their "correct" result is a magic constant the stable approximation doesn't
// model; see the skip list. TestHarte6502 compares final state + cycle COUNT;
// TestHarte6502BusTrace (issue #400) compares the full per-cycle bus trace.
// TestHarte65C02 (issue #426) runs the wdc65c02 set against VariantCMOS65C02 —
// state + cycle count; WAI/STP are skipped (halts) and invalid-BCD decimal
// ADC/SBC cases are dropped per-case (chip-specific undefined results).

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

// harteSkip lists 6502 NMOS opcodes whose Tom Harte cases chippy is not
// expected to pass:
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

// harteSkip65C02 lists WDC 65C02 opcodes chippy is not expected to match
// against the wdc65c02 set. Populated empirically (see TestHarte65C02).
var harteSkip65C02 = map[byte]string{
	0xCB: "WAI (halts until IRQ/NMI — not a single-step final state)",
	0xDB: "STP (halts until reset — not a single-step final state)",
}

// harteSuite describes one ProcessorTests data set: its repo subpath under the
// pinned commit, the CPU variant to run it against, and the opcodes to skip.
type harteSuite struct {
	label   string // human label + download-cache namespace
	subpath string // repo path under harteCommit, e.g. "6502/v1"
	envDir  string // env var pointing at a local copy of the data dir
	variant Variant
	skip    map[byte]string
	// skipCase, when non-nil, drops individual cases an opcode is not
	// expected to match (vs. skip, which drops a whole opcode).
	skipCase func(op byte, tc *harteCase) bool
}

var harte6502 = harteSuite{
	label:   "6502",
	subpath: "6502/v1",
	envDir:  "CHIPPY_HARTE_DIR",
	variant: VariantNMOS,
	skip:    harteSkip,
}

var harte65C02 = harteSuite{
	label:    "wdc65c02",
	subpath:  "wdc65c02/v1",
	envDir:   "CHIPPY_HARTE_65C02_DIR",
	variant:  VariantCMOS65C02,
	skip:     harteSkip65C02,
	skipCase: cmosDecimalInvalidBCD,
}

// cmosDecimalInvalidBCD reports whether a decimal-mode ADC/SBC case uses an
// invalid-BCD operand (a nibble > 9). The 65C02's result and flags for such
// inputs are documented-undefined and chip-specific; chippy implements the
// canonical Bruce Clark valid-BCD algorithm, which Tom Harte's wdc65c02 set
// confirms matches silicon for every VALID-BCD case. Only the invalid-BCD
// cases diverge, so they are dropped (the "data-dependent" cases). The
// effective operand is resolved with the production addressing path.
func cmosDecimalInvalidBCD(op byte, tc *harteCase) bool {
	if tc.Initial.P&0x08 == 0 { // decimal mode off
		return false
	}
	in := OpcodesCMOS[op]
	if in.Name != "ADC" && in.Name != "SBC" {
		return false
	}
	ram := NewRAM()
	for _, kv := range tc.Initial.RAM {
		ram.Write(uint16(kv[0]), byte(kv[1]))
	}
	c := NewVariant(ram, VariantCMOS65C02)
	c.PC = uint16(tc.Initial.PC) + 1 // resolve expects PC past the opcode byte
	c.A, c.X, c.Y = byte(tc.Initial.A), byte(tc.Initial.X), byte(tc.Initial.Y)
	addr, _ := c.resolve(in.Mode)
	badBCD := func(b byte) bool { return b&0x0F > 9 || b>>4 > 9 }
	return badBCD(byte(tc.Initial.A)) || badBCD(ram.Read(addr))
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

func TestHarte6502(t *testing.T)  { runHarteSuite(t, harte6502) }
func TestHarte65C02(t *testing.T) { runHarteSuite(t, harte65C02) }

func runHarteSuite(t *testing.T, suite harteSuite) {
	maxCases := 0
	if v := os.Getenv("CHIPPY_HARTE_MAX_CASES"); v != "" {
		maxCases, _ = strconv.Atoi(v)
	}
	for op := 0; op < 256; op++ {
		op := byte(op)
		name := fmt.Sprintf("%02x", op)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if reason, skip := suite.skip[op]; skip {
				t.Skipf("skip %02x: %s", op, reason)
			}
			cases, err := loadHarteCases(t, suite, name)
			if err != nil {
				t.Skipf("cases for %02x unavailable: %v", op, err)
			}
			runHarteOpcode(t, suite, op, name, cases, maxCases)
		})
	}
}

func runHarteOpcode(t *testing.T, suite harteSuite, opByte byte, op string, cases []harteCase, maxCases int) {
	t.Helper()
	n := len(cases)
	if maxCases > 0 && maxCases < n {
		n = maxCases
	}
	skipped := 0
	for i := 0; i < n; i++ {
		tc := &cases[i]
		if suite.skipCase != nil && suite.skipCase(opByte, tc) {
			skipped++
			continue
		}
		ram := NewRAM()
		for _, kv := range tc.Initial.RAM {
			ram.Write(uint16(kv[0]), byte(kv[1]))
		}
		c := NewVariant(ram, suite.variant)
		c.PC = uint16(tc.Initial.PC)
		c.SP = byte(tc.Initial.S)
		c.A, c.X, c.Y = byte(tc.Initial.A), byte(tc.Initial.X), byte(tc.Initial.Y)
		c.P = byte(tc.Initial.P)

		cyc := c.Step()

		if diff := harteDiff(c, ram, tc, cyc); diff != "" {
			t.Fatalf("opcode %s case %q (#%d):\n%s", op, tc.Name, i, diff)
		}
	}
	if skipped > 0 {
		t.Logf("opcode %s: ran %d/%d cases (%d invalid-BCD decimal cases dropped)", op, n-skipped, n, skipped)
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

// loadHarteCases returns the cases for one opcode, from the suite's local data
// dir (env var) if set, otherwise downloading (and caching) the pinned JSON.
func loadHarteCases(t *testing.T, suite harteSuite, op string) ([]harteCase, error) {
	t.Helper()
	var data []byte
	var err error
	if dir := os.Getenv(suite.envDir); dir != "" {
		data, err = os.ReadFile(filepath.Join(dir, op+".json"))
	} else {
		data, err = harteDownload(t, suite, op)
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

func harteDownload(t *testing.T, suite harteSuite, op string) ([]byte, error) {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	cacheDir = filepath.Join(cacheDir, "chippy-tests", "harte-"+suite.label, harteCommit[:12])
	cachePath := filepath.Join(cacheDir, op+".json")
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/TomHarte/ProcessorTests/%s/%s/%s.json",
		harteCommit, suite.subpath, op)
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

// harteBusSkip extends harteSkip with opcodes whose per-cycle bus TRACE (not
// just final state) diverges. As of #428 chippy passes all 238 6502 opcodes
// bus-exact, so this is empty: the branch page-cross dummy read now targets
// the pre-fixup address (old PCH | new PCL), and JSR/RTS interleave the
// operand fetch / stack ops in silicon order. Entries land here only when a
// new divergence is found.
var harteBusSkip = map[byte]string{}

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
			cases, err := loadHarteCases(t, harte6502, name)
			if err != nil {
				t.Skipf("cases for %02x unavailable: %v", op, err)
			}
			runHarteBusTrace(t, harte6502, nil, name, cases, maxCases)
		})
	}
}

func runHarteBusTrace(t *testing.T, suite harteSuite, skipCase func(byte, *harteCase) bool, op string, cases []harteCase, maxCases int) {
	t.Helper()
	opNum, _ := strconv.ParseUint(op, 16, 8)
	opByte := byte(opNum)
	n := len(cases)
	if maxCases > 0 && maxCases < n {
		n = maxCases
	}
	for i := 0; i < n; i++ {
		tc := &cases[i]
		// Drop per-case bus-trace divergences (e.g. CMOS decimal ADC/SBC, whose
		// +1 internal cycle the per-cycle interleave doesn't model).
		if skipCase != nil && skipCase(opByte, tc) {
			continue
		}
		bus := &busRecorder{}
		for _, kv := range tc.Initial.RAM {
			bus.ram[kv[0]] = byte(kv[1])
		}
		// Run the per-cycle interleave on the suite's variant so every access
		// (incl. dummy cycles) flows through the recording bus. NMOS keeps
		// decimal/quirk semantics; CMOS65C02 exercises the corrected RMW /
		// branch / JMP timings (#426).
		c := NewVariant(NewRAM(), suite.variant)
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

// harteBusSkip65C02 is the CMOS sibling of harteBusSkip: 65C02 opcodes whose
// per-cycle bus TRACE diverges from the wdc65c02 set. Mirrors #428's 6502
// work for #455.
//
// Empty: every 65C02 opcode is now per-cycle bus-exact. The CMOS-only dummy
// cycles are all modeled — RMW dummy-read, indexed page-cross, JMP indirect,
// push/pull, the WDC NOPs (#455), and BBR/BBS's dummy write-back + always-on
// branch-target read (#475). WAI/STP are skipped by harteSkip65C02 (they halt)
// and decimal ADC/SBC per-case by cmosDecimalADCSBC (the per-cycle path
// doesn't emit the BCD-correction cycle's bus access).
var harteBusSkip65C02 = map[byte]string{}

// TestHarte65C02BusTrace validates 65C02 per-cycle bus exactness against the
// wdc65c02 set (issue #455) — the CMOS sibling of TestHarte6502BusTrace.
func TestHarte65C02BusTrace(t *testing.T) {
	maxCases := 0
	if v := os.Getenv("CHIPPY_HARTE_MAX_CASES"); v != "" {
		maxCases, _ = strconv.Atoi(v)
	}
	for op := 0; op < 256; op++ {
		op := byte(op)
		name := fmt.Sprintf("%02x", op)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if reason, skip := harteSkip65C02[op]; skip {
				t.Skipf("skip %02x: %s", op, reason)
			}
			if reason, skip := harteBusSkip65C02[op]; skip {
				t.Skipf("skip %02x bus trace: %s", op, reason)
			}
			cases, err := loadHarteCases(t, harte65C02, name)
			if err != nil {
				t.Skipf("cases for %02x unavailable: %v", op, err)
			}
			runHarteBusTrace(t, harte65C02, cmosDecimalADCSBC, name, cases, maxCases)
		})
	}
}

// cmosDecimalADCSBC reports whether a case is a decimal-mode ADC/SBC — skipped
// for the 65C02 bus trace (issue #455). The 65C02 spends an extra internal
// cycle correcting the BCD result; chippy's per-cycle interleave accounts for
// that cycle in the count (TestHarte65C02 passes) but does not emit its bus
// access, so the per-cycle TRACE for these can't be compared. Final state +
// cycle count are still validated by TestHarte65C02; only the bus trace skips.
func cmosDecimalADCSBC(op byte, tc *harteCase) bool {
	if tc.Initial.P&0x08 == 0 { // decimal mode off
		return false
	}
	name := OpcodesCMOS[op].Name
	return name == "ADC" || name == "SBC"
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
