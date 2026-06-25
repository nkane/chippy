//go:build harte

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

// 65816 Tom Harte harness (#456). The 65816 set lives on a newer commit than
// the 6502/65C02 pin and is split per mode: <op>.e.json (emulation) and
// <op>.n.json (native). State is 16-bit (a/x/y/s/d) plus dbr/pbr/e; the cycle
// list uses 24-bit addresses + pin-flag strings, so we validate final STATE +
// cycle COUNT here (the per-cycle pin trace is a later chunk). Memory is a
// sparse 24-bit map — the 65816 core's own address space.
const harteCommit816 = "dff67125c4aa0a15f928769b7cbc85b2ead6c6ad"

// chunk1Opcodes816 are the register / flag / transfer / immediate instructions
// implemented in step816 chunk 1 (no data-memory addressing). Expanded as
// later chunks land.
var chunk1Opcodes816 = []byte{
	0xEA, 0x18, 0x38, 0x58, 0x78, 0xD8, 0xF8, 0xB8,
	0xE8, 0xCA, 0xC8, 0x88, 0x1A, 0x3A,
	0xAA, 0xA8, 0xBA, 0x8A, 0x98, 0x9A, 0x9B, 0xBB, 0x5B, 0x7B, 0x1B, 0x3B, 0xEB,
	0xA9, 0xA2, 0xA0, 0x29, 0x09, 0x49, 0xC9, 0xE0, 0xC0, 0x89,
	0xFB, 0xE2, 0xC2,
}

// chunk2Opcodes816 are the data-movement / ALU memory ops (all addressing
// modes): ORA/AND/EOR/ADC/SBC/STA/LDA/CMP, LDX/LDY/STX/STY/STZ, CPX/CPY, BIT.
var chunk2Opcodes816 = []byte{
	0x01, 0x03, 0x05, 0x07, 0x0D, 0x0F, 0x11, 0x12, 0x13, 0x15, 0x17, 0x19, 0x1D, 0x1F,
	0x21, 0x23, 0x25, 0x27, 0x2D, 0x2F, 0x31, 0x32, 0x33, 0x35, 0x37, 0x39, 0x3D, 0x3F,
	0x41, 0x43, 0x45, 0x47, 0x4D, 0x4F, 0x51, 0x52, 0x53, 0x55, 0x57, 0x59, 0x5D, 0x5F,
	0x61, 0x63, 0x65, 0x67, 0x69, 0x6D, 0x6F, 0x71, 0x72, 0x73, 0x75, 0x77, 0x79, 0x7D, 0x7F,
	0x81, 0x83, 0x85, 0x87, 0x8D, 0x8F, 0x91, 0x92, 0x93, 0x95, 0x97, 0x99, 0x9D, 0x9F,
	0xA1, 0xA3, 0xA5, 0xA7, 0xAD, 0xAF, 0xB1, 0xB2, 0xB3, 0xB5, 0xB7, 0xB9, 0xBD, 0xBF,
	0xC1, 0xC3, 0xC5, 0xC7, 0xCD, 0xCF, 0xD1, 0xD2, 0xD3, 0xD5, 0xD7, 0xD9, 0xDD, 0xDF,
	0xE1, 0xE3, 0xE5, 0xE7, 0xE9, 0xED, 0xEF, 0xF1, 0xF2, 0xF3, 0xF5, 0xF7, 0xF9, 0xFD, 0xFF,
	0xA6, 0xAE, 0xB6, 0xBE, 0xA4, 0xAC, 0xB4, 0xBC,
	0x86, 0x8E, 0x96, 0x84, 0x8C, 0x94,
	0x64, 0x74, 0x9C, 0x9E,
	0xE4, 0xEC, 0xC4, 0xCC,
	0x24, 0x2C, 0x34, 0x3C,
}

type mem24 map[uint32]byte

func (m mem24) Read24(a uint32) byte     { return m[a&0xFFFFFF] }
func (m mem24) Write24(a uint32, v byte) { m[a&0xFFFFFF] = v }

type harte816State struct {
	PC, S, P, A, X, Y, D int
	DBR, PBR, E          int
	RAM                  [][2]int // [24-bit addr, value]
}

type harte816Case struct {
	Name    string          `json:"name"`
	Initial harte816State   `json:"initial"`
	Final   harte816State   `json:"final"`
	Cycles  [][]interface{} `json:"cycles"`
}

func TestHarte65816(t *testing.T) {
	maxCases := 0
	if v := os.Getenv("CHIPPY_HARTE_MAX_CASES"); v != "" {
		maxCases, _ = strconv.Atoi(v)
	}
	ops := append(append([]byte{}, chunk1Opcodes816...), chunk2Opcodes816...)
	for _, op := range ops {
		op := op
		for _, mode := range []string{"e", "n"} {
			mode := mode
			name := fmt.Sprintf("%02x.%s", op, mode)
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				cases, err := loadHarte816(t, name)
				if err != nil {
					t.Skipf("cases for %s unavailable: %v", name, err)
				}
				runHarte816(t, name, cases, maxCases)
			})
		}
	}
}

func runHarte816(t *testing.T, op string, cases []harte816Case, maxCases int) {
	t.Helper()
	n := len(cases)
	if maxCases > 0 && maxCases < n {
		n = maxCases
	}
	for i := 0; i < n; i++ {
		tc := &cases[i]
		mem := mem24{}
		for _, kv := range tc.Initial.RAM {
			mem[uint32(kv[0])] = byte(kv[1])
		}
		c := NewVariant(NewRAM(), VariantW65816) // dummy 16-bit bus; 65816 uses bus24
		c.SetBus24(mem)
		ini := &tc.Initial
		c.E = ini.E == 1
		c.PC = uint16(ini.PC)
		c.setSP16(uint16(ini.S))
		c.P = byte(ini.P)
		c.setA16(uint16(ini.A))
		c.setX16(uint16(ini.X))
		c.setY16(uint16(ini.Y))
		c.D = uint16(ini.D)
		c.DBR, c.PBR = byte(ini.DBR), byte(ini.PBR)

		cyc := c.Step()

		if diff := harte816Diff(c, mem, tc, cyc); diff != "" {
			t.Fatalf("opcode %s case %q (#%d):\n%s", op, tc.Name, i, diff)
		}
	}
}

func harte816Diff(c *CPU, mem mem24, tc *harte816Case, cyc int) string {
	f := &tc.Final
	switch {
	case c.PC != uint16(f.PC):
		return fmt.Sprintf("  PC  got %04X want %04X", c.PC, f.PC)
	case c.SP16() != uint16(f.S):
		return fmt.Sprintf("  S   got %04X want %04X", c.SP16(), f.S)
	case c.A16() != uint16(f.A):
		return fmt.Sprintf("  A   got %04X want %04X", c.A16(), f.A)
	case c.X16() != uint16(f.X):
		return fmt.Sprintf("  X   got %04X want %04X", c.X16(), f.X)
	case c.Y16() != uint16(f.Y):
		return fmt.Sprintf("  Y   got %04X want %04X", c.Y16(), f.Y)
	case c.P != byte(f.P):
		return fmt.Sprintf("  P   got %08b want %08b", c.P, byte(f.P))
	case c.D != uint16(f.D):
		return fmt.Sprintf("  D   got %04X want %04X", c.D, f.D)
	case c.DBR != byte(f.DBR):
		return fmt.Sprintf("  DBR got %02X want %02X", c.DBR, f.DBR)
	case c.PBR != byte(f.PBR):
		return fmt.Sprintf("  PBR got %02X want %02X", c.PBR, f.PBR)
	case (c.E && f.E != 1) || (!c.E && f.E == 1):
		return fmt.Sprintf("  E   got %v want %d", c.E, f.E)
	case cyc != len(tc.Cycles):
		return fmt.Sprintf("  CYC got %d want %d", cyc, len(tc.Cycles))
	}
	for _, kv := range f.RAM {
		if got := mem[uint32(kv[0])]; got != byte(kv[1]) {
			return fmt.Sprintf("  RAM[%06X] got %02X want %02X", kv[0], got, byte(kv[1]))
		}
	}
	return ""
}

func loadHarte816(t *testing.T, name string) ([]harte816Case, error) {
	t.Helper()
	var data []byte
	var err error
	if dir := os.Getenv("CHIPPY_HARTE_65816_DIR"); dir != "" {
		data, err = os.ReadFile(filepath.Join(dir, name+".json"))
	} else {
		data, err = harte816Download(t, name)
	}
	if err != nil {
		return nil, err
	}
	var cases []harte816Case
	if err := json.Unmarshal(data, &cases); err != nil {
		return nil, err
	}
	return cases, nil
}

func harte816Download(t *testing.T, name string) ([]byte, error) {
	t.Helper()
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	cacheDir = filepath.Join(cacheDir, "chippy-tests", "harte-65816", harteCommit816[:12])
	cachePath := filepath.Join(cacheDir, name+".json")
	if data, err := os.ReadFile(cachePath); err == nil {
		return data, nil
	}
	url := fmt.Sprintf("https://raw.githubusercontent.com/TomHarte/ProcessorTests/%s/65816/v1/%s.json",
		harteCommit816, name)
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
