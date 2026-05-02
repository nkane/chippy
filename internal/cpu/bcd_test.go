package cpu

import "testing"

// Helper: drop the CPU into BCD mode with A and C preset, run a single
// ADC #imm or SBC #imm, return the final A and the C flag for assertions.
func runBCD(op byte, a, imm byte, carryIn bool) (out byte, cOut, vOut, nOut, zOut bool) {
	c, _ := newTestCPU([]byte{op, imm})
	c.A = a
	c.setFlag(FlagD, true)
	c.setFlag(FlagC, carryIn)
	c.Step()
	return c.A, c.hasFlag(FlagC), c.hasFlag(FlagV), c.hasFlag(FlagN), c.hasFlag(FlagZ)
}

func TestBCD_ADC_AcceptanceVectors(t *testing.T) {
	cases := []struct {
		name             string
		a, imm           byte
		carryIn          bool
		wantA            byte
		wantC            bool
	}{
		// From issue acceptance criteria.
		{"15+27=42 no carry", 0x15, 0x27, false, 0x42, false},
		{"99+01=00 carry", 0x99, 0x01, false, 0x00, true},
		// Spot-check Bruce Clark canonical row.
		{"00+00=00 no carry", 0x00, 0x00, false, 0x00, false},
		{"00+00+1=01 no carry", 0x00, 0x00, true, 0x01, false},
		{"50+50=00 carry", 0x50, 0x50, false, 0x00, true}, // 100 -> 00 + C
		{"79+00=79 no carry", 0x79, 0x00, false, 0x79, false},
		{"24+56=80 no carry", 0x24, 0x56, false, 0x80, false},
		{"93+82=75 carry", 0x93, 0x82, false, 0x75, true}, // 175 BCD = 1|75
		{"89+76+1=66 carry", 0x89, 0x76, true, 0x66, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, c, _, _, _ := runBCD(0x69, tc.a, tc.imm, tc.carryIn) // ADC #imm
			if out != tc.wantA || c != tc.wantC {
				t.Errorf("ADC %02X+%02X+C=%v -> A=%02X C=%v, want A=%02X C=%v",
					tc.a, tc.imm, tc.carryIn, out, c, tc.wantA, tc.wantC)
			}
		})
	}
}

func TestBCD_SBC_AcceptanceVectors(t *testing.T) {
	// Note: SBC carry-in semantics — C=1 means "no borrow", C=0 means borrow.
	cases := []struct {
		name             string
		a, imm           byte
		carryIn          bool
		wantA            byte
		wantC            bool
	}{
		// From issue acceptance criteria.
		{"50-25=25 no borrow", 0x50, 0x25, true, 0x25, true},
		// Bruce Clark spot-checks.
		{"00-00=00 no borrow", 0x00, 0x00, true, 0x00, true},
		{"00-01=99 borrow-out", 0x00, 0x01, true, 0x99, false},
		{"00-00-1=99 borrow-out", 0x00, 0x00, false, 0x99, false},
		{"99-00=99 no borrow", 0x99, 0x00, true, 0x99, true},
		{"50-49=01 no borrow", 0x50, 0x49, true, 0x01, true},
		{"50-50=00 no borrow", 0x50, 0x50, true, 0x00, true},
		{"30-25=05 no borrow", 0x30, 0x25, true, 0x05, true},
		{"00-99=01 borrow-out", 0x00, 0x99, true, 0x01, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, c, _, _, _ := runBCD(0xE9, tc.a, tc.imm, tc.carryIn) // SBC #imm
			if out != tc.wantA || c != tc.wantC {
				t.Errorf("SBC %02X-%02X-(1-C),C=%v -> A=%02X C=%v, want A=%02X C=%v",
					tc.a, tc.imm, tc.carryIn, out, c, tc.wantA, tc.wantC)
			}
		})
	}
}

// Exhaustive carry sweep — every (A,operand,Cin) for ADC must produce a result
// whose two BCD nibbles encode (A_dec + operand_dec + Cin) mod 100, with C
// set when the decimal sum >= 100. This validates A and C across the whole
// valid-BCD input space (10*10*2 = 200 cases).
func TestBCD_ADC_FullValidSweep(t *testing.T) {
	for ai := 0; ai < 100; ai++ {
		for vi := 0; vi < 100; vi++ {
			for _, cin := range []bool{false, true} {
				a := byte((ai/10)<<4 | (ai % 10))
				v := byte((vi/10)<<4 | (vi % 10))
				ci := 0
				if cin {
					ci = 1
				}
				expDec := (ai + vi + ci) % 100
				expA := byte((expDec/10)<<4 | (expDec % 10))
				expC := (ai + vi + ci) >= 100

				out, gotC, _, _, _ := runBCD(0x69, a, v, cin)
				if out != expA || gotC != expC {
					t.Fatalf("ADC %02X+%02X+%d -> A=%02X C=%v; want A=%02X C=%v",
						a, v, ci, out, gotC, expA, expC)
				}
			}
		}
	}
}

// Same exhaustive sweep for SBC. Decimal result = (A_dec - V_dec - (1-Cin)) mod 100,
// C = 1 when no borrow occurred (i.e. A_dec - V_dec - (1-Cin) >= 0).
func TestBCD_SBC_FullValidSweep(t *testing.T) {
	for ai := 0; ai < 100; ai++ {
		for vi := 0; vi < 100; vi++ {
			for _, cin := range []bool{false, true} {
				a := byte((ai/10)<<4 | (ai % 10))
				v := byte((vi/10)<<4 | (vi % 10))
				borrowIn := 0
				if !cin {
					borrowIn = 1
				}
				diff := ai - vi - borrowIn
				expC := diff >= 0
				expDec := ((diff % 100) + 100) % 100
				expA := byte((expDec/10)<<4 | (expDec % 10))

				out, gotC, _, _, _ := runBCD(0xE9, a, v, cin)
				if out != expA || gotC != expC {
					t.Fatalf("SBC %02X-%02X-(1-%d) -> A=%02X C=%v; want A=%02X C=%v",
						a, v, borrowIn, out, gotC, expA, expC)
				}
			}
		}
	}
}

// Binary-mode behaviour must be unchanged when D=0. Re-run the existing
// overflow case explicitly with D forced off to guard against regressions.
func TestBCD_BinaryModePreserved(t *testing.T) {
	// LDA #$50 ; ADC #$50 with D=0 -> $A0, V=1, N=1, C=0
	c, _ := newTestCPU([]byte{0xA9, 0x50, 0x69, 0x50})
	c.setFlag(FlagD, false)
	c.Step()
	c.Step()
	if c.A != 0xA0 {
		t.Fatalf("A=%02X want A0", c.A)
	}
	if !c.hasFlag(FlagV) || !c.hasFlag(FlagN) || c.hasFlag(FlagC) {
		t.Fatalf("flags wrong: V=%v N=%v C=%v",
			c.hasFlag(FlagV), c.hasFlag(FlagN), c.hasFlag(FlagC))
	}
}

func TestBCD_DecimalFlagPreservedAcrossInstruction(t *testing.T) {
	// SED ; LDA #$15 ; CLC ; ADC #$27 — exact form from issue acceptance.
	c, _ := newTestCPU([]byte{0xF8, 0xA9, 0x15, 0x18, 0x69, 0x27})
	c.Step() // SED
	c.Step() // LDA #$15
	c.Step() // CLC
	c.Step() // ADC #$27
	if c.A != 0x42 || c.hasFlag(FlagC) {
		t.Fatalf("SED;LDA#15;CLC;ADC#27 -> A=%02X C=%v, want A=42 C=false",
			c.A, c.hasFlag(FlagC))
	}
}
