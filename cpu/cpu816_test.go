package cpu

import "testing"

// sparseBus is a 24-bit test memory for the 65816 core.
type sparseBus map[uint32]byte

func (m sparseBus) Read24(a uint32) byte     { return m[a&0xFFFFFF] }
func (m sparseBus) Write24(a uint32, v byte) { m[a&0xFFFFFF] = v }

// new816 builds a 65816 CPU executing prog at bank 0 / $8000 through a sparse
// 24-bit bus (the 65816 core's own address space).
func new816(prog ...byte) (*CPU, sparseBus) {
	mem := sparseBus{}
	for i, b := range prog {
		mem[uint32(0x8000+i)] = b
	}
	c := NewVariant(NewRAM(), VariantW65816)
	c.SetBus24(mem)
	c.PC = 0x8000
	return c, mem
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
}

func TestW65816_RegisterAccessors(t *testing.T) {
	c, _ := new816(0xEA)
	c.E = false // native: stack high byte not forced
	c.setA16(0x1234)
	c.setX16(0x5678)
	c.setY16(0x9ABC)
	c.setSP16(0xDEF0)
	if c.A16() != 0x1234 || c.X16() != 0x5678 || c.Y16() != 0x9ABC || c.SP16() != 0xDEF0 {
		t.Fatalf("16-bit accessors: A=%04X X=%04X Y=%04X SP=%04X",
			c.A16(), c.X16(), c.Y16(), c.SP16())
	}
	// Emulation locks the stack high byte to $01.
	c.E = true
	c.setSP16(0xDEF0)
	if c.SP16() != 0x01F0 {
		t.Fatalf("emulation SP high must lock to $01, got %04X", c.SP16())
	}
}

func TestW65816_EmulationBaseOpLeavesAccumulatorHighByte(t *testing.T) {
	// LDA #$42 in emulation mode touches only the low byte; the 16-bit
	// accumulator high byte (CPU.B) is preserved.
	c, _ := new816(0xA9, 0x42)
	c.B = 0x99
	c.Step()
	if c.A != 0x42 {
		t.Fatalf("LDA #$42: A should be $42, got $%02X", c.A)
	}
	if c.B != 0x99 {
		t.Fatalf("emulation-mode LDA must leave accumulator high byte intact, B=$%02X", c.B)
	}
}

func TestW65816_LDAImmediate16BitNative(t *testing.T) {
	// Native mode, 16-bit accumulator (M=0): LDA # reads two bytes.
	c, _ := new816(0xA9, 0x34, 0x12)
	c.E = false
	c.P &^= FlagM // 16-bit accumulator
	c.Step()
	if c.A16() != 0x1234 {
		t.Fatalf("native 16-bit LDA #$1234: A=%04X", c.A16())
	}
	if c.PC != 0x8003 {
		t.Fatalf("16-bit immediate should consume 2 operand bytes, PC=%04X", c.PC)
	}
}

func TestW65816_XCETogglesEmulationAndCarry(t *testing.T) {
	c, _ := new816(0xFB, 0xFB)
	c.P &^= FlagC // C=0
	c.Step()      // XCE: C=oldE=1, E=oldC=0 -> native
	if c.E {
		t.Fatalf("XCE with C=0 should enter native mode (E=0)")
	}
	if c.P&FlagC == 0 {
		t.Fatalf("XCE should put old E (1) into carry")
	}
	c.Step() // XCE: C=1 -> E=1 (emulation), C=oldE=0
	if !c.E {
		t.Fatalf("second XCE (C=1) should return to emulation mode (E=1)")
	}
	if c.P&FlagC != 0 {
		t.Fatalf("second XCE should clear carry (old E was 0)")
	}
	if c.SPHi != 0x01 {
		t.Fatalf("re-entering emulation should force stack high byte to $01")
	}
}

func TestW65816_SEPREPSetClearFlags(t *testing.T) {
	c, _ := new816(0xE2, 0x20, 0xC2, 0x20) // SEP #$20 ; REP #$20
	c.E = false                            // native so M/X aren't locked
	c.Step()                               // SEP #$20 -> set bit 5 (M)
	if c.P&FlagM == 0 {
		t.Fatalf("SEP #$20 should set the M bit")
	}
	c.Step() // REP #$20 -> clear bit 5
	if c.P&FlagM != 0 {
		t.Fatalf("REP #$20 should clear the M bit")
	}
}

func TestW65816_EmulationLocksMXBits(t *testing.T) {
	c, _ := new816(0xC2, 0x30) // REP #$30 (try to clear bits 4+5)
	c.P |= FlagM | FlagX
	c.Step()
	if c.P&FlagM == 0 || c.P&FlagX == 0 {
		t.Fatalf("emulation mode must lock M/X; REP #$30 cleared them: P=$%02X", c.P)
	}
}

func TestW65816_BlockMoveMVN(t *testing.T) {
	// MVN $00,$00 moves C+1 bytes from src:X (ascending) to dst:Y. Source
	// $2000.. holds 1,2,3,4; move 4 bytes to $3000.
	c, mem := new816(0x54, 0x00, 0x00)
	c.E = false
	c.PC = 0x8000
	for i, b := range []byte{1, 2, 3, 4} {
		mem[uint32(0x2000+i)] = b
	}
	c.setX16(0x2000)
	c.setY16(0x3000)
	c.setA16(3) // C = count-1 → 4 bytes
	c.Step()
	for i, want := range []byte{1, 2, 3, 4} {
		if got := mem[uint32(0x3000+i)]; got != want {
			t.Fatalf("MVN dst[%d]=$%02X want $%02X", i, got, want)
		}
	}
	if c.X16() != 0x2004 || c.Y16() != 0x3004 {
		t.Fatalf("MVN should advance X/Y by 4: X=%04X Y=%04X", c.X16(), c.Y16())
	}
	if c.A16() != 0xFFFF {
		t.Fatalf("MVN should leave C=$FFFF, got %04X", c.A16())
	}
}

func TestW65816_DisasmWidthAndModes(t *testing.T) {
	ram := NewRAM()
	c := NewVariant(ram, VariantW65816)
	c.E = false

	// LDA # is 16-bit (3 bytes) when M=0, 8-bit (2 bytes) when M=1.
	ram.Write(0x1000, 0xA9)
	ram.Write(0x1001, 0x34)
	ram.Write(0x1002, 0x12)
	c.P &^= FlagM
	if txt, n := DisasmCPU(c, 0x1000); txt != "LDA  #$1234" || n != 3 {
		t.Fatalf("16-bit immediate: %q n=%d", txt, n)
	}
	c.P |= FlagM
	if txt, n := DisasmCPU(c, 0x1000); txt != "LDA  #$34" || n != 2 {
		t.Fatalf("8-bit immediate: %q n=%d", txt, n)
	}

	// LDA long (24-bit).
	ram.Write(0x2000, 0xAF)
	ram.Write(0x2001, 0x56)
	ram.Write(0x2002, 0x34)
	ram.Write(0x2003, 0x12)
	if txt, n := DisasmCPU(c, 0x2000); txt != "LDA  $123456" || n != 4 {
		t.Fatalf("long: %q n=%d", txt, n)
	}

	// [dp] and MVN render the 65816-specific syntaxes.
	ram.Write(0x3000, 0xA7)
	ram.Write(0x3001, 0x10)
	if txt, _ := DisasmCPU(c, 0x3000); txt != "LDA  [$10]" {
		t.Fatalf("[dp]: %q", txt)
	}
	ram.Write(0x3100, 0x54) // MVN dst=$02, src=$01 -> "MVN $01,$02"
	ram.Write(0x3101, 0x02)
	ram.Write(0x3102, 0x01)
	if txt, n := DisasmCPU(c, 0x3100); txt != "MVN  $01,$02" || n != 3 {
		t.Fatalf("MVN: %q n=%d", txt, n)
	}
}

func TestW65816_Bus24From16Bridge(t *testing.T) {
	ram := NewRAM()
	c := NewVariant(ram, VariantW65816)
	c.SetBus24(Bus24From16(ram))
	ram.Write(0x8000, 0xA9) // LDA #$42
	ram.Write(0x8001, 0x42)
	c.PC = 0x8000
	c.Step()
	if c.A != 0x42 {
		t.Fatalf("bank-0 bridge LDA: A=$%02X want $42", c.A)
	}
}

func TestBanked24_BankIsolation(t *testing.T) {
	ram := NewRAM()
	b := NewBanked24(ram)

	// Bank 0 routes through the 16-bit chain: a Banked24 write is visible on the
	// underlying RAM and vice-versa.
	b.Write24(0x000010, 0x11)
	if got := ram.Read(0x0010); got != 0x11 {
		t.Fatalf("bank-0 write should hit the 16-bit chain: ram[$0010]=$%02X want $11", got)
	}
	ram.Write(0x0020, 0x22)
	if got := b.Read24(0x000020); got != 0x22 {
		t.Fatalf("bank-0 read should see the 16-bit chain: $%02X want $22", got)
	}

	// Banks 1-255 are distinct storage — the same offset in different banks does
	// not alias (the bug the bank-0 mirror had).
	b.Write24(0x010010, 0xAA)
	b.Write24(0x020010, 0xBB)
	if got := b.Read24(0x010010); got != 0xAA {
		t.Fatalf("bank 1 offset $0010 = $%02X want $AA", got)
	}
	if got := b.Read24(0x020010); got != 0xBB {
		t.Fatalf("bank 2 offset $0010 = $%02X want $BB", got)
	}
	// And a banked write never leaks into bank 0.
	if got := ram.Read(0x0010); got != 0x11 {
		t.Fatalf("banked write leaked into bank 0: ram[$0010]=$%02X want $11", got)
	}
}

func TestW65816_IRQEmulation(t *testing.T) {
	c, mem := new816(0xEA) // NOP at $8000; resets in emulation mode
	mem[0xFFFE] = 0x00
	mem[0xFFFF] = 0x90 // emulation IRQ vector -> $9000
	c.setSP16(0x01FF)
	c.setFlag(FlagI, false) // I clear so the IRQ is taken
	c.setFlag(FlagD, true)  // must be cleared by interrupt entry
	c.AssertIRQ()

	c.Step()

	if c.PC != 0x9000 {
		t.Fatalf("IRQ emu vector: PC=$%04X want $9000", c.PC)
	}
	if !c.hasFlag(FlagI) {
		t.Fatal("I should be set after interrupt entry")
	}
	if c.hasFlag(FlagD) {
		t.Fatal("D should be cleared on interrupt entry")
	}
	// Emulation stack: PCH@$01FF, PCL@$01FE, P@$01FD; SP -> $01FC.
	if c.SP16() != 0x01FC {
		t.Fatalf("SP=$%04X want $01FC", c.SP16())
	}
	if mem[0x01FF] != 0x80 || mem[0x01FE] != 0x00 {
		t.Fatalf("pushed PC wrong: hi=$%02X lo=$%02X want 80 00", mem[0x01FF], mem[0x01FE])
	}
	if p := mem[0x01FD]; p&FlagB != 0 {
		t.Fatalf("hardware IRQ must push P with B clear, got $%02X", p)
	}
}

func TestW65816_NMINative(t *testing.T) {
	c, mem := new816(0xEA)
	c.E = false       // native mode
	c.PBR = 0x12      // running in bank $12
	c.setSP16(0x1FFF) // native stack outside page 1
	mem[0xFFEA] = 0x34
	mem[0xFFEB] = 0x56 // native NMI vector -> $5634
	c.TriggerNMI()

	c.Step()

	if c.PC != 0x5634 {
		t.Fatalf("NMI native vector: PC=$%04X want $5634", c.PC)
	}
	if c.PBR != 0 {
		t.Fatalf("PBR should be 0 in the handler, got $%02X", c.PBR)
	}
	// Native stack: PBR@$1FFF, PCH@$1FFE, PCL@$1FFD, P@$1FFC; SP -> $1FFB.
	if c.SP16() != 0x1FFB {
		t.Fatalf("SP=$%04X want $1FFB", c.SP16())
	}
	if mem[0x1FFF] != 0x12 {
		t.Fatalf("native interrupt must push PBR, got $%02X want $12", mem[0x1FFF])
	}
	if mem[0x1FFE] != 0x80 || mem[0x1FFD] != 0x00 {
		t.Fatalf("pushed PC wrong: hi=$%02X lo=$%02X want 80 00", mem[0x1FFE], mem[0x1FFD])
	}
}

func TestW65816_BlockMoveMVP(t *testing.T) {
	// MVP moves descending. Move 3 bytes ending at src $2002 -> dst $3002.
	c, mem := new816(0x44, 0x00, 0x00)
	c.E = false
	c.PC = 0x8000
	for i, b := range []byte{0xAA, 0xBB, 0xCC} {
		mem[uint32(0x2000+i)] = b
	}
	c.setX16(0x2002)
	c.setY16(0x3002)
	c.setA16(2) // 3 bytes
	c.Step()
	for i, want := range []byte{0xAA, 0xBB, 0xCC} {
		if got := mem[uint32(0x3000+i)]; got != want {
			t.Fatalf("MVP dst[%d]=$%02X want $%02X", i, got, want)
		}
	}
	if c.X16() != 0x1FFF || c.Y16() != 0x2FFF {
		t.Fatalf("MVP should retreat X/Y past the start: X=%04X Y=%04X", c.X16(), c.Y16())
	}
}
