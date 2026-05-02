package cpu

import "testing"

// Helper: load a program at $8000 and pre-seed A/X/Y/P/some RAM.
func setupIllegal(prog []byte, setup func(*CPU, *RAM)) (*CPU, *RAM) {
	c, r := newTestCPU(prog)
	if setup != nil {
		setup(c, r)
	}
	return c, r
}

// --- LAX ---

func TestLAX_ZP(t *testing.T) {
	// LAX $40
	c, _ := setupIllegal([]byte{0xA7, 0x40}, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x7F)
	})
	c.Step()
	if c.A != 0x7F || c.X != 0x7F {
		t.Fatalf("A=%02X X=%02X want 7F/7F", c.A, c.X)
	}
	if c.hasFlag(FlagZ) || c.hasFlag(FlagN) {
		t.Fatalf("flags wrong: %08b", c.P)
	}
}

func TestLAX_SetsNegative(t *testing.T) {
	c, _ := setupIllegal([]byte{0xA7, 0x40}, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x80)
	})
	c.Step()
	if c.A != 0x80 || c.X != 0x80 {
		t.Fatalf("A=%02X X=%02X want 80/80", c.A, c.X)
	}
	if !c.hasFlag(FlagN) {
		t.Fatalf("N not set")
	}
}

// --- SAX ---

func TestSAX_ZP(t *testing.T) {
	// LDA #$F0 ; LDX #$0F ; SAX $50  -> $50 := $F0 & $0F = $00
	prog := []byte{0xA9, 0xF0, 0xA2, 0x0F, 0x87, 0x50}
	c, r := newTestCPU(prog)
	c.Step()
	c.Step()
	pBefore := c.P
	c.Step()
	if r.Read(0x0050) != 0x00 {
		t.Fatalf("$50=%02X want 00", r.Read(0x0050))
	}
	if c.P != pBefore {
		t.Fatalf("SAX modified flags: before=%08b after=%08b", pBefore, c.P)
	}
}

// --- DCP ---

func TestDCP_DecAndCompareEqual(t *testing.T) {
	// LDA #$05 ; DCP $40  ; $40 was $06 -> becomes $05, CMP A($05) vs $05 -> Z=1, C=1
	prog := []byte{0xA9, 0x05, 0xC7, 0x40}
	c, r := setupIllegal(prog, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x06)
	})
	c.Step()
	c.Step()
	if r.Read(0x0040) != 0x05 {
		t.Fatalf("mem=%02X want 05", r.Read(0x0040))
	}
	if !c.hasFlag(FlagZ) || !c.hasFlag(FlagC) {
		t.Fatalf("flags %08b want Z+C", c.P)
	}
}

// --- ISC ---

func TestISC_IncAndSubtract(t *testing.T) {
	// LDA #$10 ; SEC ; ISC $40  ; $40 was $04 -> becomes $05, A := $10 - $05 = $0B
	prog := []byte{0xA9, 0x10, 0x38, 0xE7, 0x40}
	c, r := setupIllegal(prog, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x04)
	})
	c.Step()
	c.Step()
	c.Step()
	if r.Read(0x0040) != 0x05 {
		t.Fatalf("mem=%02X want 05", r.Read(0x0040))
	}
	if c.A != 0x0B {
		t.Fatalf("A=%02X want 0B", c.A)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set (no borrow)")
	}
}

// --- SLO ---

func TestSLO_ShiftAndOR(t *testing.T) {
	// LDA #$01 ; SLO $40  ; $40=$81 -> shifted=$02, C=1; A := $01 | $02 = $03
	prog := []byte{0xA9, 0x01, 0x07, 0x40}
	c, r := setupIllegal(prog, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x81)
	})
	c.Step()
	c.Step()
	if r.Read(0x0040) != 0x02 {
		t.Fatalf("mem=%02X want 02", r.Read(0x0040))
	}
	if c.A != 0x03 {
		t.Fatalf("A=%02X want 03", c.A)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set from old bit 7")
	}
}

// --- RLA ---

func TestRLA_RotateAndAND(t *testing.T) {
	// CLC ; LDA #$0F ; RLA $40  ; $40=$81 -> rotated=$02 (carry-in 0), A := $0F & $02 = $02
	prog := []byte{0x18, 0xA9, 0x0F, 0x27, 0x40}
	c, r := setupIllegal(prog, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x81)
	})
	c.Step()
	c.Step()
	c.Step()
	if r.Read(0x0040) != 0x02 {
		t.Fatalf("mem=%02X want 02", r.Read(0x0040))
	}
	if c.A != 0x02 {
		t.Fatalf("A=%02X want 02", c.A)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set from old bit 7")
	}
}

// --- SRE ---

func TestSRE_ShiftRightAndEOR(t *testing.T) {
	// LDA #$F0 ; SRE $40  ; $40=$03 -> shifted=$01, C=1; A := $F0 ^ $01 = $F1
	prog := []byte{0xA9, 0xF0, 0x47, 0x40}
	c, r := setupIllegal(prog, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x03)
	})
	c.Step()
	c.Step()
	if r.Read(0x0040) != 0x01 {
		t.Fatalf("mem=%02X want 01", r.Read(0x0040))
	}
	if c.A != 0xF1 {
		t.Fatalf("A=%02X want F1", c.A)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set from old bit 0")
	}
}

// --- RRA ---

func TestRRA_RotateRightAndADC(t *testing.T) {
	// CLC ; LDA #$10 ; RRA $40  ; $40=$02 -> rotated right=$01, C=0
	// Then ADC: A := $10 + $01 + 0 = $11
	prog := []byte{0x18, 0xA9, 0x10, 0x67, 0x40}
	c, r := setupIllegal(prog, func(c *CPU, r *RAM) {
		r.Write(0x0040, 0x02)
	})
	c.Step()
	c.Step()
	c.Step()
	if r.Read(0x0040) != 0x01 {
		t.Fatalf("mem=%02X want 01", r.Read(0x0040))
	}
	if c.A != 0x11 {
		t.Fatalf("A=%02X want 11", c.A)
	}
}

// --- ANC ---

func TestANC_CopiesNegToCarry(t *testing.T) {
	// LDA #$FF ; ANC #$80  -> A := $80, N=1, C=1
	prog := []byte{0xA9, 0xFF, 0x0B, 0x80}
	c, _ := newTestCPU(prog)
	c.Step()
	c.Step()
	if c.A != 0x80 {
		t.Fatalf("A=%02X want 80", c.A)
	}
	if !c.hasFlag(FlagN) || !c.hasFlag(FlagC) {
		t.Fatalf("flags %08b want N+C", c.P)
	}
}

func TestANC_ZeroResult(t *testing.T) {
	// LDA #$0F ; ANC #$F0  -> A := 0, Z=1, C=0
	prog := []byte{0xA9, 0x0F, 0x0B, 0xF0}
	c, _ := newTestCPU(prog)
	c.Step()
	c.Step()
	if c.A != 0x00 {
		t.Fatalf("A=%02X want 00", c.A)
	}
	if !c.hasFlag(FlagZ) || c.hasFlag(FlagC) {
		t.Fatalf("flags %08b want Z, no C", c.P)
	}
}

// --- ALR ---

func TestALR_AndThenShiftRight(t *testing.T) {
	// LDA #$FF ; ALR #$03  -> A & $03 = $03, then >>1 = $01, C=1 (old bit 0)
	prog := []byte{0xA9, 0xFF, 0x4B, 0x03}
	c, _ := newTestCPU(prog)
	c.Step()
	c.Step()
	if c.A != 0x01 {
		t.Fatalf("A=%02X want 01", c.A)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set")
	}
}

// --- SBX ---

func TestSBX_NoBorrow(t *testing.T) {
	// LDA #$FF ; LDX #$0F ; SBX #$01  -> ($FF & $0F)=$0F; $0F - $01 = $0E, C=1
	prog := []byte{0xA9, 0xFF, 0xA2, 0x0F, 0xCB, 0x01}
	c, _ := newTestCPU(prog)
	c.Step()
	c.Step()
	c.Step()
	if c.X != 0x0E {
		t.Fatalf("X=%02X want 0E", c.X)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set (no borrow)")
	}
	if c.A != 0xFF {
		t.Fatalf("SBX modified A: %02X", c.A)
	}
}

func TestSBX_WithBorrow(t *testing.T) {
	// LDA #$01 ; LDX #$01 ; SBX #$05  -> X := ($01 & $01) - $05 = $FC, C=0
	prog := []byte{0xA9, 0x01, 0xA2, 0x01, 0xCB, 0x05}
	c, _ := newTestCPU(prog)
	c.Step()
	c.Step()
	c.Step()
	if c.X != 0xFC {
		t.Fatalf("X=%02X want FC", c.X)
	}
	if c.hasFlag(FlagC) {
		t.Fatalf("C should be clear (borrow occurred)")
	}
	if !c.hasFlag(FlagN) {
		t.Fatalf("N should be set")
	}
}

// --- SBC alias at $EB ---

func TestSBC_AliasEB(t *testing.T) {
	// SEC ; LDA #$10 ; SBC #$01 (via $EB)  -> A := $0F, C=1
	prog := []byte{0x38, 0xA9, 0x10, 0xEB, 0x01}
	c, _ := newTestCPU(prog)
	c.Step()
	c.Step()
	c.Step()
	if c.A != 0x0F {
		t.Fatalf("A=%02X want 0F", c.A)
	}
	if !c.hasFlag(FlagC) {
		t.Fatalf("C should be set")
	}
}

// --- multi-byte NOPs advance PC correctly ---

func TestNOP_Multibyte_AdvancesPC(t *testing.T) {
	cases := []struct {
		name   string
		prog   []byte
		wantPC uint16
	}{
		{"1-byte $1A", []byte{0x1A}, 0x8001},
		{"2-byte IMM $80", []byte{0x80, 0xFF}, 0x8002},
		{"2-byte ZP $04", []byte{0x04, 0x40}, 0x8002},
		{"2-byte ZPX $14", []byte{0x14, 0x40}, 0x8002},
		{"3-byte ABS $0C", []byte{0x0C, 0x34, 0x12}, 0x8003},
		{"3-byte ABX $1C", []byte{0x1C, 0x34, 0x12}, 0x8003},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestCPU(tc.prog)
			pBefore := c.P
			aBefore, xBefore, yBefore := c.A, c.X, c.Y
			c.Step()
			if c.PC != tc.wantPC {
				t.Fatalf("PC=%04X want %04X", c.PC, tc.wantPC)
			}
			if c.P != pBefore {
				t.Fatalf("NOP modified flags")
			}
			if c.A != aBefore || c.X != xBefore || c.Y != yBefore {
				t.Fatalf("NOP modified registers")
			}
		})
	}
}
