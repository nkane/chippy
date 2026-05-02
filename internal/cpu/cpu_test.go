package cpu

import "testing"

func newTestCPU(prog []byte) (*CPU, *RAM) {
	r := NewRAM()
	r.Load(0x8000, prog)
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x80)
	return New(r), r
}

func TestLDA_Immediate(t *testing.T) {
	c, _ := newTestCPU([]byte{0xA9, 0x42})
	c.Step()
	if c.A != 0x42 {
		t.Fatalf("A=%02X want 42", c.A)
	}
	if c.hasFlag(FlagZ) || c.hasFlag(FlagN) {
		t.Fatalf("flags wrong: %08b", c.P)
	}
}

func TestADC_BasicAndOverflow(t *testing.T) {
	// LDA #$50 ; ADC #$50 -> result $A0, V=1, N=1
	c, _ := newTestCPU([]byte{0xA9, 0x50, 0x69, 0x50})
	c.Step()
	c.Step()
	if c.A != 0xA0 {
		t.Fatalf("A=%02X want A0", c.A)
	}
	if !c.hasFlag(FlagV) {
		t.Fatalf("V not set")
	}
	if !c.hasFlag(FlagN) {
		t.Fatalf("N not set")
	}
}

func TestJSR_RTS(t *testing.T) {
	// $8000: JSR $8005 ; LDA #$01 ; BRK
	// $8005: LDA #$AA ; RTS
	prog := []byte{
		0x20, 0x05, 0x80, // JSR $8005
		0xA9, 0x01,       // LDA #$01
		0xA9, 0xAA, 0x60, // pad+ LDA #$AA ; RTS at $8005,$8006,$8007 -- adjust
	}
	// Actually rebuild precisely:
	prog = []byte{
		0x20, 0x06, 0x80, // 8000: JSR $8006
		0xA9, 0x01,       // 8003: LDA #$01
		0x00,             // 8005: BRK
		0xA9, 0xAA,       // 8006: LDA #$AA
		0x60,             // 8008: RTS
	}
	c, _ := newTestCPU(prog)
	c.Step() // JSR
	if c.PC != 0x8006 {
		t.Fatalf("PC=%04X want 8006", c.PC)
	}
	c.Step() // LDA #$AA
	if c.A != 0xAA {
		t.Fatalf("A=%02X want AA", c.A)
	}
	c.Step() // RTS
	if c.PC != 0x8003 {
		t.Fatalf("PC=%04X want 8003", c.PC)
	}
	c.Step() // LDA #$01
	if c.A != 0x01 {
		t.Fatalf("A=%02X want 01", c.A)
	}
}

func TestBranch_BNE_Taken(t *testing.T) {
	// LDX #$01 ; DEX ; BNE -2 (loops once it hits 0 falls thru)
	prog := []byte{
		0xA2, 0x02, // LDX #$02
		0xCA,       // DEX
		0xD0, 0xFD, // BNE -3 (back to DEX)
	}
	c, _ := newTestCPU(prog)
	c.Step() // LDX
	for i := 0; i < 10; i++ {
		c.Step() // DEX
		c.Step() // BNE
		if c.X == 0 {
			break
		}
	}
	if c.X != 0 {
		t.Fatalf("X=%02X want 0", c.X)
	}
}
