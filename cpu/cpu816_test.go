package cpu

import "testing"

// new816 builds a 65816 CPU with a one-instruction program at $8000 and the
// reset vector pointing there.
func new816(prog ...byte) (*CPU, *RAM) {
	ram := NewRAM()
	ram.Load(0x8000, prog)
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	c := NewVariant(ram, VariantW65816)
	return c, ram
}

func TestW65816_ResetIsEmulationMode(t *testing.T) {
	c, _ := new816(0xEA)
	if !c.E {
		t.Fatalf("65816 should power up in emulation mode (E=1)")
	}
	if c.SPHi != 0x01 {
		t.Fatalf("emulation stack high byte should be $01, got $%02X", c.SPHi)
	}
	if c.Variant.String() != "65816" {
		t.Fatalf("variant string: want 65816, got %q", c.Variant.String())
	}
	if c.opcodes != &Opcodes65816 {
		t.Fatalf("65816 should bind the Opcodes65816 table")
	}
}

func TestW65816_EmulationBaseOpLeavesAccumulatorHighByte(t *testing.T) {
	// LDA #$42 in emulation mode touches only the low byte; the 16-bit
	// accumulator high byte (CPU.B) is preserved.
	c, _ := new816(0xA9, 0x42)
	c.B = 0x99 // pretend a prior 16-bit op left a high byte
	c.PC = 0x8000
	c.Step()
	if c.A != 0x42 {
		t.Fatalf("LDA #$42: A should be $42, got $%02X", c.A)
	}
	if c.B != 0x99 {
		t.Fatalf("emulation-mode LDA must leave accumulator high byte intact, B=$%02X", c.B)
	}
}

func TestW65816_XCETogglesEmulationAndCarry(t *testing.T) {
	// Reset: E=1, C=0. XCE -> C=oldE=1, E=oldC=0 (enter native).
	c, ram := new816(0xFB, 0xFB)
	c.PC = 0x8000
	c.P &^= FlagC // C=0
	c.Step()      // XCE
	if c.E {
		t.Fatalf("XCE with C=0 should enter native mode (E=0)")
	}
	if c.P&FlagC == 0 {
		t.Fatalf("XCE should put old E (1) into carry")
	}
	_ = ram
	// XCE again: C=1 -> E=1 (back to emulation), C=oldE=0.
	c.Step()
	if !c.E {
		t.Fatalf("second XCE (C=1) should return to emulation mode (E=1)")
	}
	if c.P&FlagC != 0 {
		t.Fatalf("second XCE should clear carry (old E was 0)")
	}
	if c.SPHi != 0x01 {
		t.Fatalf("re-entering emulation should reset stack high byte to $01")
	}
}

func TestW65816_SEPREPSetClearFlags(t *testing.T) {
	// Native mode so the M/X width bits aren't locked.
	c, _ := new816(0xE2, 0x20, 0xC2, 0x20) // SEP #$20 ; REP #$20
	c.PC = 0x8000
	c.E = false // native

	c.Step() // SEP #$20 -> set bit 5 (M)
	if c.P&FlagU == 0 {
		t.Fatalf("SEP #$20 should set P bit 5")
	}
	c.Step() // REP #$20 -> clear bit 5
	if c.P&FlagU != 0 {
		t.Fatalf("REP #$20 should clear P bit 5")
	}
}

func TestW65816_EmulationLocksMXBits(t *testing.T) {
	// In emulation mode REP must not clear the M/X (bit 5/4) flags.
	c, _ := new816(0xC2, 0x30) // REP #$30 (try to clear bits 4+5)
	c.PC = 0x8000
	c.P |= FlagU | FlagB
	c.Step()
	if c.P&FlagU == 0 || c.P&FlagB == 0 {
		t.Fatalf("emulation mode must lock M/X (bits 5/4); REP #$30 cleared them: P=$%02X", c.P)
	}
}
