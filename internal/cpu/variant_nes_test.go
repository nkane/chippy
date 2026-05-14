package cpu

import "testing"

// VariantNES (the Ricoh 2A03) lacks decimal-mode arithmetic. ADC/SBC
// ignore FlagD even when set — the bit still toggles via SED/CLD but
// the BCD adder is missing in silicon.

func newNES(prog []byte) (*CPU, *RAM) {
	return newTestCPUVariant(VariantNES, prog)
}

// SED + ADC #$09 + #$01: NMOS BCD math says A=$10. 2A03 / VariantNES:
// FlagD is set but ignored, so A=$0A (binary).
func TestVariantNES_ADC_IgnoresDecimalFlag(t *testing.T) {
	// SED ; LDA #$09 ; ADC #$01
	c, _ := newNES([]byte{0xF8, 0xA9, 0x09, 0x69, 0x01})
	c.Step() // SED
	if !c.hasFlag(FlagD) {
		t.Fatalf("SED should set D")
	}
	c.Step() // LDA #$09 → A=$09
	c.Step() // ADC #$01 → A should be $0A (binary), not $10 (BCD)
	if c.A != 0x0A {
		t.Fatalf("VariantNES ADC under D=1 should be binary: A=$%02X want $0A", c.A)
	}
	// Flag still set so program code can probe it.
	if !c.hasFlag(FlagD) {
		t.Fatalf("FlagD should still be set; only the adder ignores it")
	}
}

// Same shape for SBC. NMOS BCD: 0x10 - 0x01 = 0x09. 2A03 binary:
// 0x10 - 0x01 = 0x0F.
func TestVariantNES_SBC_IgnoresDecimalFlag(t *testing.T) {
	// SED ; SEC ; LDA #$10 ; SBC #$01
	c, _ := newNES([]byte{0xF8, 0x38, 0xA9, 0x10, 0xE9, 0x01})
	c.Step() // SED
	c.Step() // SEC
	c.Step() // LDA #$10
	c.Step() // SBC #$01 — binary: 0x10 - 0x01 = 0x0F
	if c.A != 0x0F {
		t.Fatalf("VariantNES SBC under D=1 should be binary: A=$%02X want $0F", c.A)
	}
}

// NMOS BCD on the same program: A should be $10 (decimal $10 - $01
// = $09 ... wait — SBC #$01 with carry=1 means no borrow → 16 - 1 = 15
// in binary OR 0x10 - 0x01 = 0x09 in BCD). Pin the NMOS behavior so
// the NES test above isn't a tautology.
func TestVariantNMOS_SBC_BCDStillWorks(t *testing.T) {
	c, _ := newTestCPU([]byte{0xF8, 0x38, 0xA9, 0x10, 0xE9, 0x01})
	c.Step()
	c.Step()
	c.Step()
	c.Step()
	if c.A != 0x09 {
		t.Fatalf("NMOS SBC under D=1 should be BCD: A=$%02X want $09", c.A)
	}
}

// Variant.String() prints "nes" for the new variant.
func TestVariantNES_StringName(t *testing.T) {
	if VariantNES.String() != "nes" {
		t.Fatalf("VariantNES.String() = %q; want %q", VariantNES.String(), "nes")
	}
}

// NES opcode table is just NMOS — every official opcode runs and
// Klaus 6502 functional behaviors hold (the Klaus harness lives under
// a build tag so this test just smokes a few representative ops).
func TestVariantNES_OpcodesMatchNMOS(t *testing.T) {
	// LDA #$42 ; TAX ; INX ; STX $0200
	c, r := newNES([]byte{0xA9, 0x42, 0xAA, 0xE8, 0x8E, 0x00, 0x02})
	for i := 0; i < 4; i++ {
		c.Step()
	}
	if c.A != 0x42 || c.X != 0x43 || r.Read(0x0200) != 0x43 {
		t.Fatalf("VariantNES NMOS-shape ops wrong: A=$%02X X=$%02X $0200=$%02X",
			c.A, c.X, r.Read(0x0200))
	}
}
