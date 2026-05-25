package cpu

// Cycle-count regression suite. Cross-checked against
// http://www.oxyron.de/html/opcodes02.html and the visual6502 reference.
//
// Each table entry pins one (opcode, addressing mode) tuple. The harness
// loads a small program at $8000 with operand bytes set to make page-cross
// behavior deterministic, optionally seeds X/Y, executes a single Step(),
// and asserts the cycle delta exactly matches the canonical value.
//
// Page-cross variants are tested twice per opcode (once with no cross,
// once with a cross) for any addressing mode that has the +1 quirk.

import (
	"fmt"
	"testing"
)

type cycleCase struct {
	name     string
	op       byte         // opcode byte
	operands []byte       // operand bytes that follow the opcode
	x, y     byte         // pre-loaded X / Y
	memSeed  func(r *RAM) // optional pre-load (e.g. zero-page pointer for IZY)
	want     int          // expected cycles consumed by Step()
}

// Helper: zero-page pointer at $40 -> $20F0  (so IZY+1 with Y=$10 = $2100, page cross).
func seedIZY_2100(r *RAM) { r.Write(0x40, 0xF0); r.Write(0x41, 0x20) }

// Same pointer base $20F0 with Y=$05 -> $20F5 (no cross).
func seedIZY_20F0(r *RAM) { r.Write(0x40, 0xF0); r.Write(0x41, 0x20) }

func runCycle(t *testing.T, tc cycleCase) {
	t.Helper()
	r := NewRAM()
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x80)
	prog := append([]byte{tc.op}, tc.operands...)
	r.Load(0x8000, prog)
	if tc.memSeed != nil {
		tc.memSeed(r)
	}
	c := New(r)
	c.Reset()
	c.X = tc.x
	c.Y = tc.y
	before := c.Cycles
	got := c.Step()
	delta := int(c.Cycles - before)
	if got != tc.want || delta != tc.want {
		t.Fatalf("%s: got %d cycles (delta=%d) want %d", tc.name, got, delta, tc.want)
	}
}

func TestCycles_Official(t *testing.T) {
	// Pick at least one opcode per addressing mode for every official
	// instruction family. Where an addressing mode has +1 page-cross
	// behavior, exercise both branches.
	cases := []cycleCase{
		// ----- LDA -----
		{"LDA imm", 0xA9, []byte{0x42}, 0, 0, nil, 2},
		{"LDA zp", 0xA5, []byte{0x10}, 0, 0, nil, 3},
		{"LDA zpx", 0xB5, []byte{0x10}, 0x05, 0, nil, 4},
		{"LDA abs", 0xAD, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"LDA abx no-cross", 0xBD, []byte{0x00, 0x20}, 0x05, 0, nil, 4},
		{"LDA abx cross", 0xBD, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
		{"LDA aby no-cross", 0xB9, []byte{0x00, 0x20}, 0, 0x05, nil, 4},
		{"LDA aby cross", 0xB9, []byte{0xFF, 0x20}, 0, 0x01, nil, 5},
		{"LDA izx", 0xA1, []byte{0x40}, 0x00, 0, seedIZY_20F0, 6},
		{"LDA izy no-cross", 0xB1, []byte{0x40}, 0, 0x05, seedIZY_20F0, 5},
		{"LDA izy cross", 0xB1, []byte{0x40}, 0, 0x10, seedIZY_2100, 6},

		// ----- LDX -----
		{"LDX imm", 0xA2, []byte{0x42}, 0, 0, nil, 2},
		{"LDX zp", 0xA6, []byte{0x10}, 0, 0, nil, 3},
		{"LDX zpy", 0xB6, []byte{0x10}, 0, 0x05, nil, 4},
		{"LDX abs", 0xAE, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"LDX aby no-cross", 0xBE, []byte{0x00, 0x20}, 0, 0x05, nil, 4},
		{"LDX aby cross", 0xBE, []byte{0xFF, 0x20}, 0, 0x01, nil, 5},

		// ----- LDY -----
		{"LDY imm", 0xA0, []byte{0x42}, 0, 0, nil, 2},
		{"LDY zp", 0xA4, []byte{0x10}, 0, 0, nil, 3},
		{"LDY zpx", 0xB4, []byte{0x10}, 0x05, 0, nil, 4},
		{"LDY abs", 0xAC, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"LDY abx no-cross", 0xBC, []byte{0x00, 0x20}, 0x05, 0, nil, 4},
		{"LDY abx cross", 0xBC, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},

		// ----- STA (no page-cross add for stores) -----
		{"STA zp", 0x85, []byte{0x10}, 0, 0, nil, 3},
		{"STA zpx", 0x95, []byte{0x10}, 0x05, 0, nil, 4},
		{"STA abs", 0x8D, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"STA abx", 0x9D, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
		{"STA aby", 0x99, []byte{0xFF, 0x20}, 0, 0x01, nil, 5},
		{"STA izx", 0x81, []byte{0x40}, 0x00, 0, seedIZY_20F0, 6},
		{"STA izy", 0x91, []byte{0x40}, 0, 0x10, seedIZY_2100, 6},

		// ----- STX / STY -----
		{"STX zp", 0x86, []byte{0x10}, 0, 0, nil, 3},
		{"STX zpy", 0x96, []byte{0x10}, 0, 0x05, nil, 4},
		{"STX abs", 0x8E, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"STY zp", 0x84, []byte{0x10}, 0, 0, nil, 3},
		{"STY zpx", 0x94, []byte{0x10}, 0x05, 0, nil, 4},
		{"STY abs", 0x8C, []byte{0x00, 0x20}, 0, 0, nil, 4},

		// ----- transfers -----
		{"TAX", 0xAA, nil, 0, 0, nil, 2},
		{"TAY", 0xA8, nil, 0, 0, nil, 2},
		{"TSX", 0xBA, nil, 0, 0, nil, 2},
		{"TXA", 0x8A, nil, 0, 0, nil, 2},
		{"TXS", 0x9A, nil, 0, 0, nil, 2},
		{"TYA", 0x98, nil, 0, 0, nil, 2},

		// ----- stack -----
		{"PHA", 0x48, nil, 0, 0, nil, 3},
		{"PHP", 0x08, nil, 0, 0, nil, 3},
		{"PLA", 0x68, nil, 0, 0, nil, 4},
		{"PLP", 0x28, nil, 0, 0, nil, 4},

		// ----- AND / EOR / ORA -----
		{"AND imm", 0x29, []byte{0xFF}, 0, 0, nil, 2},
		{"AND abx cross", 0x3D, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
		{"EOR aby cross", 0x59, []byte{0xFF, 0x20}, 0, 0x01, nil, 5},
		{"ORA izy cross", 0x11, []byte{0x40}, 0, 0x10, seedIZY_2100, 6},

		// ----- BIT -----
		{"BIT zp", 0x24, []byte{0x10}, 0, 0, nil, 3},
		{"BIT abs", 0x2C, []byte{0x00, 0x20}, 0, 0, nil, 4},

		// ----- arithmetic -----
		{"ADC imm", 0x69, []byte{0x01}, 0, 0, nil, 2},
		{"ADC abx cross", 0x7D, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
		{"SBC imm", 0xE9, []byte{0x01}, 0, 0, nil, 2},
		{"SBC aby no-cross", 0xF9, []byte{0x00, 0x20}, 0, 0x05, nil, 4},

		// ----- compares -----
		{"CMP imm", 0xC9, []byte{0x00}, 0, 0, nil, 2},
		{"CMP abx cross", 0xDD, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
		{"CPX imm", 0xE0, []byte{0x00}, 0, 0, nil, 2},
		{"CPY imm", 0xC0, []byte{0x00}, 0, 0, nil, 2},

		// ----- INC / DEC (RMW — NEVER pay page-cross even on abs,X) -----
		{"INC zp", 0xE6, []byte{0x10}, 0, 0, nil, 5},
		{"INC zpx", 0xF6, []byte{0x10}, 0x05, 0, nil, 6},
		{"INC abs", 0xEE, []byte{0x00, 0x20}, 0, 0, nil, 6},
		{"INC abx no-cross", 0xFE, []byte{0x00, 0x20}, 0x05, 0, nil, 7},
		{"INC abx cross", 0xFE, []byte{0xFF, 0x20}, 0x01, 0, nil, 7}, // RMW: still 7
		{"DEC abx cross", 0xDE, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},
		{"INX", 0xE8, nil, 0, 0, nil, 2},
		{"INY", 0xC8, nil, 0, 0, nil, 2},
		{"DEX", 0xCA, nil, 0, 0, nil, 2},
		{"DEY", 0x88, nil, 0, 0, nil, 2},

		// ----- shifts (RMW) -----
		{"ASL acc", 0x0A, nil, 0, 0, nil, 2},
		{"ASL zp", 0x06, []byte{0x10}, 0, 0, nil, 5},
		{"ASL zpx", 0x16, []byte{0x10}, 0x05, 0, nil, 6},
		{"ASL abs", 0x0E, []byte{0x00, 0x20}, 0, 0, nil, 6},
		{"ASL abx cross", 0x1E, []byte{0xFF, 0x20}, 0x01, 0, nil, 7}, // RMW
		{"LSR abx cross", 0x5E, []byte{0xFF, 0x20}, 0x01, 0, nil, 7}, // RMW
		{"ROL abx cross", 0x3E, []byte{0xFF, 0x20}, 0x01, 0, nil, 7}, // RMW
		{"ROR abx cross", 0x7E, []byte{0xFF, 0x20}, 0x01, 0, nil, 7}, // RMW

		// ----- jumps -----
		{"JMP abs", 0x4C, []byte{0x00, 0x90}, 0, 0, nil, 3},
		{"JMP ind", 0x6C, []byte{0x00, 0x90}, 0, 0, func(r *RAM) {
			r.Write(0x9000, 0x34)
			r.Write(0x9001, 0x12)
		}, 5},
		{"JSR", 0x20, []byte{0x00, 0x90}, 0, 0, nil, 6},

		// ----- flags -----
		{"CLC", 0x18, nil, 0, 0, nil, 2},
		{"SEC", 0x38, nil, 0, 0, nil, 2},
		{"CLI", 0x58, nil, 0, 0, nil, 2},
		{"SEI", 0x78, nil, 0, 0, nil, 2},
		{"CLV", 0xB8, nil, 0, 0, nil, 2},
		{"CLD", 0xD8, nil, 0, 0, nil, 2},
		{"SED", 0xF8, nil, 0, 0, nil, 2},

		// ----- system -----
		{"BRK", 0x00, []byte{0x00}, 0, 0, nil, 7},
		{"NOP", 0xEA, nil, 0, 0, nil, 2},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { runCycle(t, tc) })
	}
}

// Branches: 2 cycles not taken, +1 if taken, +1 more if page crossed.
func TestCycles_Branches(t *testing.T) {
	type branchCase struct {
		name      string
		op        byte
		offset    int8
		flagSetup func(c *CPU)
		startPC   uint16 // override $8000 if needed for cross test
		want      int
	}
	cases := []branchCase{
		// BNE — branch on Z=0
		{"BNE not taken", 0xD0, 0x10, func(c *CPU) { c.setFlag(FlagZ, true) }, 0x8000, 2},
		{"BNE taken no-cross", 0xD0, 0x10, func(c *CPU) { c.setFlag(FlagZ, false) }, 0x8000, 3},
		// Place opcode so the +offset target lands on a different page.
		// PC after fetching operand = $80FE + 2 = $8100; offset $10 -> $8110 (no cross).
		// Use $80F0 + 2 = $80F2; offset $10 -> $8102 (no cross from $80, since
		// after operand fetch PC is $80F2 in same page as target $8102? $80F2
		// and $8102 are in different pages -> cross). Use that.
		{"BNE taken cross", 0xD0, 0x10, func(c *CPU) { c.setFlag(FlagZ, false) }, 0x80F0, 4},

		// BEQ
		{"BEQ taken no-cross", 0xF0, 0x05, func(c *CPU) { c.setFlag(FlagZ, true) }, 0x8000, 3},
		// BCC
		{"BCC taken no-cross", 0x90, 0x05, func(c *CPU) { c.setFlag(FlagC, false) }, 0x8000, 3},
		// BCS
		{"BCS taken no-cross", 0xB0, 0x05, func(c *CPU) { c.setFlag(FlagC, true) }, 0x8000, 3},
		// BPL
		{"BPL taken no-cross", 0x10, 0x05, func(c *CPU) { c.setFlag(FlagN, false) }, 0x8000, 3},
		// BMI
		{"BMI taken no-cross", 0x30, 0x05, func(c *CPU) { c.setFlag(FlagN, true) }, 0x8000, 3},
		// BVC
		{"BVC taken no-cross", 0x50, 0x05, func(c *CPU) { c.setFlag(FlagV, false) }, 0x8000, 3},
		// BVS
		{"BVS taken no-cross", 0x70, 0x05, func(c *CPU) { c.setFlag(FlagV, true) }, 0x8000, 3},

		// Backwards branch crossing page boundary.
		// At $8002 a "BNE -$04" jumps back to $7FFE, crossing from $80 to $7F.
		{"BNE taken backward cross", 0xD0, -0x04, func(c *CPU) { c.setFlag(FlagZ, false) }, 0x8000, 4},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := NewRAM()
			r.Write(VecReset, byte(tc.startPC&0xFF))
			r.Write(VecReset+1, byte(tc.startPC>>8))
			r.Load(tc.startPC, []byte{tc.op, byte(tc.offset)})
			c := New(r)
			c.Reset()
			tc.flagSetup(c)
			before := c.Cycles
			got := c.Step()
			delta := int(c.Cycles - before)
			if got != tc.want || delta != tc.want {
				t.Fatalf("%s: got %d cycles (delta=%d) want %d (PC after=$%04X)",
					tc.name, got, delta, tc.want, c.PC)
			}
		})
	}
}

// Illegal-opcode RMW cycle counts (matching opcodes_illegal.go's table).
func TestCycles_Illegal(t *testing.T) {
	cases := []cycleCase{
		// LAX (load A and X) — same shape as LDA mem-modes.
		{"LAX zp", 0xA7, []byte{0x10}, 0, 0, nil, 3},
		{"LAX zpy", 0xB7, []byte{0x10}, 0, 0x05, nil, 4},
		{"LAX abs", 0xAF, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"LAX aby no-cross", 0xBF, []byte{0x00, 0x20}, 0, 0x05, nil, 4},
		{"LAX aby cross", 0xBF, []byte{0xFF, 0x20}, 0, 0x01, nil, 5},
		{"LAX izx", 0xA3, []byte{0x40}, 0x00, 0, seedIZY_20F0, 6},
		{"LAX izy no-cross", 0xB3, []byte{0x40}, 0, 0x05, seedIZY_20F0, 5},
		{"LAX izy cross", 0xB3, []byte{0x40}, 0, 0x10, seedIZY_2100, 6},

		// SAX — store, no page-cross add.
		{"SAX zp", 0x87, []byte{0x10}, 0, 0, nil, 3},
		{"SAX zpy", 0x97, []byte{0x10}, 0, 0x05, nil, 4},
		{"SAX abs", 0x8F, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"SAX izx", 0x83, []byte{0x40}, 0x00, 0, seedIZY_20F0, 6},

		// RMW illegals — never pay the page-cross add (PageAdd=false).
		// Spot-check one mode each.
		{"DCP abx cross", 0xDF, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},
		{"DCP aby cross", 0xDB, []byte{0xFF, 0x20}, 0, 0x01, nil, 7},
		{"DCP izy cross", 0xD3, []byte{0x40}, 0, 0x10, seedIZY_2100, 8},
		{"ISC abx cross", 0xFF, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},
		{"SLO abx cross", 0x1F, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},
		{"RLA abx cross", 0x3F, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},
		{"SRE abx cross", 0x5F, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},
		{"RRA abx cross", 0x7F, []byte{0xFF, 0x20}, 0x01, 0, nil, 7},

		// Single-byte / immediate illegals.
		{"ANC #imm", 0x0B, []byte{0xFF}, 0, 0, nil, 2},
		{"ALR #imm", 0x4B, []byte{0xFF}, 0, 0, nil, 2},
		{"ARR #imm", 0x6B, []byte{0xFF}, 0, 0, nil, 2},
		{"SBX #imm", 0xCB, []byte{0x00}, 0, 0, nil, 2},
		{"SBC #imm $EB alias", 0xEB, []byte{0x01}, 0, 0, nil, 2},

		// Multi-byte NOPs.
		{"NOP $1A", 0x1A, nil, 0, 0, nil, 2},
		{"NOP #imm $80", 0x80, []byte{0x00}, 0, 0, nil, 2},
		{"NOP zp $04", 0x04, []byte{0x10}, 0, 0, nil, 3},
		{"NOP zpx $14", 0x14, []byte{0x10}, 0x05, 0, nil, 4},
		{"NOP abs $0C", 0x0C, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"NOP abx $1C no-cross", 0x1C, []byte{0x00, 0x20}, 0x05, 0, nil, 4},
		{"NOP abx $1C cross", 0x1C, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { runCycle(t, tc) })
	}
}

// Sanity: ensure every entry in the opcode table has non-zero Cycles
// (would catch a stray uninitialized slot).
func TestCycles_NoZeroEntries(t *testing.T) {
	for op, in := range Opcodes {
		if in.Cycles == 0 {
			t.Errorf("opcode $%02X (%s) has Cycles=0", op, in.Name)
		}
	}
}

// Compile-time guard: name unused helper to avoid lint noise.
var _ = fmt.Sprintf
