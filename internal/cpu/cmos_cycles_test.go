package cpu

// CMOS 65C02 cycle audit (issue #111). Parallels the NMOS table in
// cycles_test.go. Numbers cross-checked against
// http://www.6502.org/tutorials/65c02opcodes.html and the WDC W65C02S
// datasheet. Each case exercises one (opcode, addressing mode) tuple
// under controlled operands; page-cross variants get both sides.

import "testing"

func runCMOSCycle(t *testing.T, tc cycleCase) {
	t.Helper()
	r := NewRAM()
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x80)
	prog := append([]byte{tc.op}, tc.operands...)
	r.Load(0x8000, prog)
	if tc.memSeed != nil {
		tc.memSeed(r)
	}
	c := NewVariant(r, VariantCMOS65C02)
	c.X = tc.x
	c.Y = tc.y
	before := c.Cycles
	got := c.Step()
	delta := int(c.Cycles - before)
	if got != tc.want || delta != tc.want {
		t.Fatalf("%s: got %d cycles (delta=%d) want %d", tc.name, got, delta, tc.want)
	}
}

func TestCMOSCycles_OfficialOps(t *testing.T) {
	cases := []cycleCase{
		// CMOS keeps the NMOS shape for shared mnemonics — spot-check
		// each addressing mode at least once so a regression in the
		// shared opcode table surfaces.
		{"LDA imm", 0xA9, []byte{0x42}, 0, 0, nil, 2},
		{"LDA zp", 0xA5, []byte{0x10}, 0, 0, nil, 3},
		{"LDA zpx", 0xB5, []byte{0x10}, 0x05, 0, nil, 4},
		{"LDA abs", 0xAD, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"LDA abx no-cross", 0xBD, []byte{0x00, 0x20}, 0x05, 0, nil, 4},
		{"LDA abx cross", 0xBD, []byte{0xFF, 0x20}, 0x01, 0, nil, 5},
		{"LDA aby cross", 0xB9, []byte{0xFF, 0x20}, 0, 0x01, nil, 5},
		{"STA zp", 0x85, []byte{0x10}, 0, 0, nil, 3},
		{"STA abs", 0x8D, []byte{0x00, 0x20}, 0, 0, nil, 4},

		// CMOS-only — BRA (always-branch). Same cycle profile as a
		// taken conditional: 3 no-cross, 4 cross.
		{"BRA no-cross", 0x80, []byte{0x05}, 0, 0, nil, 3},

		// INA / DEA — implicit A increment/decrement, 2 cycles.
		{"INA", 0x1A, nil, 0, 0, nil, 2},
		{"DEA", 0x3A, nil, 0, 0, nil, 2},

		// PHX / PLX / PHY / PLY.
		{"PHX", 0xDA, nil, 0, 0, nil, 3},
		{"PLX", 0xFA, nil, 0, 0, nil, 4},
		{"PHY", 0x5A, nil, 0, 0, nil, 3},
		{"PLY", 0x7A, nil, 0, 0, nil, 4},

		// STZ — store zero. Same shape as STA across modes.
		{"STZ zp", 0x64, []byte{0x10}, 0, 0, nil, 3},
		{"STZ zpx", 0x74, []byte{0x10}, 0x05, 0, nil, 4},
		{"STZ abs", 0x9C, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"STZ abx", 0x9E, []byte{0x00, 0x20}, 0x05, 0, nil, 5},

		// TRB / TSB — test-and-{set,reset} bits. 5 zp / 6 abs.
		{"TSB zp", 0x04, []byte{0x10}, 0, 0, nil, 5},
		{"TSB abs", 0x0C, []byte{0x00, 0x20}, 0, 0, nil, 6},
		{"TRB zp", 0x14, []byte{0x10}, 0, 0, nil, 5},
		{"TRB abs", 0x1C, []byte{0x00, 0x20}, 0, 0, nil, 6},

		// JMP (abs,X) — 6 cycles, no page-cross add.
		{"JMP (abs,X)", 0x7C, []byte{0x00, 0x20}, 0x05, 0, nil, 6},

		// LDA (zp) — the IZP mode. 5 cycles, no page-cross add.
		{"LDA (zp)", 0xB2, []byte{0x40}, 0, 0, seedIZY_20F0, 5},

		// RMB / SMB — bit clear / set on zero page. 5 cycles each.
		{"RMB0", 0x07, []byte{0x10}, 0, 0, nil, 5},
		{"RMB7", 0x77, []byte{0x10}, 0, 0, nil, 5},
		{"SMB0", 0x87, []byte{0x10}, 0, 0, nil, 5},
		{"SMB7", 0xF7, []byte{0x10}, 0, 0, nil, 5},

		// BBR / BBS — branch-on-bit-{reset,set}. 5 cycles base; +1 on
		// taken branch (no page-cross +1 in our model — matches WDC).
		{"BBR0 not taken", 0x0F, []byte{0x10, 0x05}, 0, 0,
			func(r *RAM) { r.Write(0x10, 0x01) /* bit 0 set → no branch */ }, 5},
		{"BBR0 taken", 0x0F, []byte{0x10, 0x05}, 0, 0,
			func(r *RAM) { r.Write(0x10, 0xFE) /* bit 0 clear → branch */ }, 6},
		{"BBS0 taken", 0x8F, []byte{0x10, 0x05}, 0, 0,
			func(r *RAM) { r.Write(0x10, 0x01) /* bit 0 set → branch */ }, 6},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { runCMOSCycle(t, tc) })
	}
}

// CMOS BCD: ADC/SBC under FlagD=1 cost +1 cycle vs NMOS, even on
// immediate-mode operands. NMOS pays 2 cycles for ADC #imm regardless
// of D; CMOS pays 3.
func TestCMOSCycles_BCDPenalty(t *testing.T) {
	cases := []struct {
		name string
		op   byte
		// CMOS expected cycles with D=1.
		want int
	}{
		{"ADC #imm D=1", 0x69, 3},
		{"ADC zp  D=1", 0x65, 4},
		{"ADC abs D=1", 0x6D, 5},
		{"SBC #imm D=1", 0xE9, 3},
		{"SBC zp  D=1", 0xE5, 4},
		{"SBC abs D=1", 0xED, 5},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := NewRAM()
			r.Write(VecReset, 0x00)
			r.Write(VecReset+1, 0x80)
			var prog []byte
			switch tc.op {
			case 0x69, 0xE9:
				prog = []byte{tc.op, 0x05}
			case 0x65, 0xE5:
				prog = []byte{tc.op, 0x10}
			default: // 0x6D / 0xED
				prog = []byte{tc.op, 0x00, 0x20}
			}
			r.Load(0x8000, prog)
			c := NewVariant(r, VariantCMOS65C02)
			c.setFlag(FlagD, true)
			before := c.Cycles
			got := c.Step()
			delta := int(c.Cycles - before)
			if got != tc.want || delta != tc.want {
				t.Fatalf("%s: got %d cycles (delta=%d) want %d", tc.name, got, delta, tc.want)
			}
		})
	}
}

// WDC-spec NOP fills. Each of the 28 "unused" NMOS opcode slots maps
// to a 1-byte / 1-cycle NOP on CMOS — except a handful documented as
// multi-byte / multi-cycle. Exercise the documented widths.
func TestCMOSCycles_NOPFills(t *testing.T) {
	cases := []cycleCase{
		// $x2 NOP slots are 2-byte / 2-cycle IMM NOPs (the slots not
		// claimed by the IZP-mode CMOS instructions like $12 / $32 /
		// $52 / $72 / $92 / $B2 / $D2 / $F2).
		{"NOP $02 (2b/2c)", 0x02, []byte{0x00}, 0, 0, nil, 2},
		{"NOP $03 (1b/1c)", 0x03, nil, 0, 0, nil, 1},
		{"NOP $13 (1b/1c)", 0x13, nil, 0, 0, nil, 1},
		// ZP-prefixed NOPs (2 bytes, 3 cycles): $44.
		{"NOP $44 (2b/3c)", 0x44, []byte{0x10}, 0, 0, nil, 3},
		// ZPX-prefixed NOPs (2 bytes, 4 cycles): $54, $D4, $F4.
		{"NOP $54 (2b/4c)", 0x54, []byte{0x10}, 0, 0, nil, 4},
		{"NOP $D4 (2b/4c)", 0xD4, []byte{0x10}, 0, 0, nil, 4},
		{"NOP $F4 (2b/4c)", 0xF4, []byte{0x10}, 0, 0, nil, 4},
		// Quirky $5C — 3 bytes, 8 cycles per WDC silicon.
		{"NOP $5C (3b/8c)", 0x5C, []byte{0x00, 0x20}, 0, 0, nil, 8},
		// ABS NOPs (3 bytes, 4 cycles): $DC, $FC.
		{"NOP $DC (3b/4c)", 0xDC, []byte{0x00, 0x20}, 0, 0, nil, 4},
		{"NOP $FC (3b/4c)", 0xFC, []byte{0x00, 0x20}, 0, 0, nil, 4},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { runCMOSCycle(t, tc) })
	}
}

// Halt opcodes (issue #122). Spec says WAI / STP each consume 3 cycles
// at the boundary they enter; subsequent Step() calls return 0 while
// halted.
func TestCMOSCycles_HaltOps(t *testing.T) {
	cases := []cycleCase{
		{"WAI", 0xCB, nil, 0, 0, nil, 3},
		{"STP", 0xDB, nil, 0, 0, nil, 3},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) { runCMOSCycle(t, tc) })
	}
}

// Sanity: every CMOS opcode slot has a non-zero Cycles field.
func TestCMOSCycles_NoZeroEntries(t *testing.T) {
	for op, in := range OpcodesCMOS {
		if in.Cycles == 0 {
			t.Errorf("CMOS opcode $%02X (%s) has Cycles=0", op, in.Name)
		}
	}
}
