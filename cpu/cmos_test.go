package cpu

import "testing"

func newTestCPUVariant(v Variant, prog []byte) (*CPU, *RAM) {
	r := NewRAM()
	r.Load(0x8000, prog)
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x80)
	return NewVariant(r, v), r
}

func newCMOS(prog []byte) (*CPU, *RAM) { return newTestCPUVariant(VariantCMOS65C02, prog) }

// --- Variant identity ---
func TestVariant_String(t *testing.T) {
	if VariantNMOS.String() != "nmos" || VariantCMOS65C02.String() != "65c02" {
		t.Fatalf("variant strings wrong")
	}
}

func TestVariant_NMOSRejectsCMOSOnly(t *testing.T) {
	// $80 on NMOS is the unofficial NOP #imm (2-byte, 2-cycle). Verify
	// that under NMOS we do NOT execute it as BRA.
	c, _ := newTestCPU([]byte{0x80, 0x10, 0xA9, 0x42}) // BRA-or-NOP, then LDA
	c.Step()
	// PC should now be at $8002 (consumed 2 bytes), A unchanged (=0).
	if c.PC != 0x8002 {
		t.Fatalf("NMOS treated $80 as branch: PC=%04X want 8002", c.PC)
	}
	if c.A != 0 {
		t.Fatalf("NMOS $80: A=%02X want 0", c.A)
	}
}

// --- BRA ---
func TestCMOS_BRA(t *testing.T) {
	// BRA +4 ; LDA #$11 (skipped) ; ... ; LDA #$22 (target)
	prog := []byte{0x80, 0x04, 0xA9, 0x11, 0x00, 0x00, 0xA9, 0x22}
	c, _ := newCMOS(prog)
	c.Step() // BRA
	if c.PC != 0x8006 {
		t.Fatalf("BRA target wrong: PC=%04X want 8006", c.PC)
	}
	c.Step() // LDA #$22
	if c.A != 0x22 {
		t.Fatalf("BRA didn't land on LDA #$22; A=%02X", c.A)
	}
}

// --- PHX/PHY/PLX/PLY ---
func TestCMOS_PHXPLX(t *testing.T) {
	// LDX #$5A ; PHX ; LDX #$00 ; PLX
	c, _ := newCMOS([]byte{0xA2, 0x5A, 0xDA, 0xA2, 0x00, 0xFA})
	for i := 0; i < 4; i++ {
		c.Step()
	}
	if c.X != 0x5A {
		t.Fatalf("PHX/PLX broken: X=%02X want 5A", c.X)
	}
}

func TestCMOS_PHYPLY(t *testing.T) {
	c, _ := newCMOS([]byte{0xA0, 0x77, 0x5A, 0xA0, 0x00, 0x7A}) // LDY/PHY/LDY/PLY
	for i := 0; i < 4; i++ {
		c.Step()
	}
	if c.Y != 0x77 {
		t.Fatalf("PHY/PLY broken: Y=%02X want 77", c.Y)
	}
}

// --- STZ ---
func TestCMOS_STZ(t *testing.T) {
	c, r := newCMOS([]byte{
		0x64, 0x10, // STZ $10
		0x9C, 0x00, 0x20, // STZ $2000
	})
	r.Write(0x10, 0xFF)
	r.Write(0x2000, 0xFF)
	c.Step()
	c.Step()
	if r.Read(0x10) != 0 || r.Read(0x2000) != 0 {
		t.Fatalf("STZ failed: zp=%02X abs=%02X", r.Read(0x10), r.Read(0x2000))
	}
}

// --- INA / DEA ---
func TestCMOS_INA_DEA(t *testing.T) {
	// LDA #$0F ; INC A ; INC A ; DEC A
	c, _ := newCMOS([]byte{0xA9, 0x0F, 0x1A, 0x1A, 0x3A})
	for i := 0; i < 4; i++ {
		c.Step()
	}
	if c.A != 0x10 {
		t.Fatalf("INA/DEA broken: A=%02X want 10", c.A)
	}
}

// --- TRB / TSB ---
func TestCMOS_TSB_TRB(t *testing.T) {
	c, r := newCMOS([]byte{
		0xA9, 0x0F, // LDA #$0F
		0x04, 0x20, // TSB $20
		0x14, 0x20, // TRB $20
	})
	r.Write(0x20, 0xF0)
	c.Step() // LDA
	c.Step() // TSB
	if r.Read(0x20) != 0xFF {
		t.Fatalf("TSB: $20=%02X want FF", r.Read(0x20))
	}
	// A=$0F, mem=$F0 -> A&mem=0 -> Z must be set.
	if !c.hasFlag(FlagZ) {
		t.Fatalf("TSB Z must be set when A&mem == 0")
	}
	c.Step() // TRB
	if r.Read(0x20) != 0xF0 {
		t.Fatalf("TRB: $20=%02X want F0", r.Read(0x20))
	}
}

// --- JMP (abs,X) ---
func TestCMOS_JMP_AbsX(t *testing.T) {
	// LDX #$04 ; JMP ($8100,X)  -- vector at $8104 -> $8200
	c, r := newCMOS([]byte{0xA2, 0x04, 0x7C, 0x00, 0x81})
	r.Write(0x8104, 0x00)
	r.Write(0x8105, 0x82)
	c.Step()
	c.Step()
	if c.PC != 0x8200 {
		t.Fatalf("JMP (abs,X) target wrong: PC=%04X want 8200", c.PC)
	}
}

// --- (zp) addressing ---
func TestCMOS_LDA_IZP(t *testing.T) {
	// pointer at $30/$31 -> $90AB; LDA ($30)
	c, r := newCMOS([]byte{0xB2, 0x30})
	r.Write(0x30, 0xAB)
	r.Write(0x31, 0x90)
	r.Write(0x90AB, 0x77)
	c.Step()
	if c.A != 0x77 {
		t.Fatalf("LDA ($30): A=%02X want 77", c.A)
	}
}

// --- JMP (ind) page-wrap fixed ---
func TestCMOS_JMP_IND_NoWrap(t *testing.T) {
	// Vector spans $80FF/$8100. NMOS would wrap to $80FF/$8000.
	// Place the JMP at $8200 so $8000 is left as 0 (NMOS wrap target hi byte).
	r := NewRAM()
	r.Load(0x8200, []byte{0x6C, 0xFF, 0x80})
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x82)
	r.Write(0x80FF, 0x34)
	r.Write(0x8100, 0x12)
	c := NewVariant(r, VariantCMOS65C02)
	c.Step()
	if c.PC != 0x1234 {
		t.Fatalf("CMOS JMP (ind) wrap: PC=%04X want 1234", c.PC)
	}
}

func TestNMOS_JMP_IND_PageWrapBugStillPresent(t *testing.T) {
	r := NewRAM()
	r.Load(0x8200, []byte{0x6C, 0xFF, 0x80})
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x82)
	r.Write(0x80FF, 0x34)
	r.Write(0x8100, 0x12)
	r.Write(0x8000, 0xAB) // NMOS reads here as hi byte instead of $8100
	c := New(r)
	c.Step()
	if c.PC != 0xAB34 {
		t.Fatalf("NMOS JMP (ind) should wrap: PC=%04X want AB34", c.PC)
	}
}

// --- BBR / BBS ---
func TestCMOS_BBR0_Branches(t *testing.T) {
	// BBR0 $40,+4 — branch if bit 0 of $40 is 0
	c, r := newCMOS([]byte{0x0F, 0x40, 0x04, 0xA9, 0x11, 0x00, 0x00, 0xA9, 0x22})
	r.Write(0x40, 0xFE) // bit 0 = 0 -> branch
	c.Step()
	if c.PC != 0x8007 {
		t.Fatalf("BBR0 branch target: PC=%04X want 8007", c.PC)
	}
}

func TestCMOS_BBS7_DoesNotBranch(t *testing.T) {
	// BBS7 = $FF; bit 7 of $40 = 0 means no branch.
	c, r := newCMOS([]byte{0xFF, 0x40, 0x10, 0xA9, 0x55})
	r.Write(0x40, 0x00)
	c.Step()
	if c.PC != 0x8003 {
		t.Fatalf("BBS7 should not branch: PC=%04X want 8003", c.PC)
	}
}

// --- RMB / SMB ---
func TestCMOS_SMB_RMB(t *testing.T) {
	c, r := newCMOS([]byte{
		0x07, 0x50, // RMB0 $50
		0x97, 0x50, // SMB1 $50
	})
	r.Write(0x50, 0xFF)
	c.Step()
	if r.Read(0x50) != 0xFE {
		t.Fatalf("RMB0 wrong: $50=%02X want FE", r.Read(0x50))
	}
	r.Write(0x50, 0x00)
	c.Step()
	if r.Read(0x50) != 0x02 {
		t.Fatalf("SMB1 wrong: $50=%02X want 02", r.Read(0x50))
	}
}

// --- BIT immediate (CMOS only modifies Z) ---
func TestCMOS_BITimm_OnlyZ(t *testing.T) {
	// Set N+V via NMOS-style BIT abs first... easier: directly poke P
	c, _ := newCMOS([]byte{0xA9, 0x0F, 0x89, 0xF0}) // LDA #$0F ; BIT #$F0
	c.Step()
	c.Step()
	if !c.hasFlag(FlagZ) {
		t.Fatalf("BIT #imm should set Z when A&imm=0")
	}
	if c.hasFlag(FlagN) {
		t.Fatalf("BIT #imm must not touch N")
	}
}
