package cpu

import "testing"

// installVector writes a little-endian vector value at addr.
func installVector(r *RAM, addr, target uint16) {
	r.Write(addr, byte(target&0xFF))
	r.Write(addr+1, byte(target>>8))
}

// --- IRQ masking ---

func TestIRQ_MaskedWhenISet(t *testing.T) {
	// Program at $8000: NOP ; NOP ; NOP
	c, r := newTestCPU([]byte{0xEA, 0xEA, 0xEA})
	installVector(r, VecIRQ, 0x9000)
	c.setFlag(FlagI, true) // mask IRQs
	c.AssertIRQ()
	c.Step()
	if c.PC != 0x8001 {
		t.Fatalf("IRQ taken while masked: PC=%04X want 8001", c.PC)
	}
}

func TestIRQ_FiresWhenIClear(t *testing.T) {
	c, r := newTestCPU([]byte{0xEA, 0xEA})
	installVector(r, VecIRQ, 0x9000)
	c.setFlag(FlagI, false)
	c.AssertIRQ()
	cyc := c.Step()
	if c.PC != 0x9000 {
		t.Fatalf("IRQ not taken: PC=%04X want 9000", c.PC)
	}
	if cyc != 7 {
		t.Fatalf("IRQ cycles wrong: %d want 7", cyc)
	}
	if !c.hasFlag(FlagI) {
		t.Fatalf("FlagI not set after IRQ dispatch")
	}
}

func TestIRQ_PushesPWithBClear(t *testing.T) {
	c, r := newTestCPU([]byte{0xEA})
	installVector(r, VecIRQ, 0x9000)
	c.setFlag(FlagI, false)
	c.setFlag(FlagN, true) // arbitrary state we expect to see preserved
	c.AssertIRQ()
	spBefore := c.SP
	c.Step()
	// Stack after IRQ: pushed PCH, PCL, P at SP+1, SP+2, SP+3 (post-decrement).
	pushedP := c.Bus.Read(0x0100 | uint16(spBefore-2))
	if pushedP&FlagB != 0 {
		t.Fatalf("IRQ pushed P with B set: %02X", pushedP)
	}
	if pushedP&FlagU == 0 {
		t.Fatalf("IRQ pushed P with U clear: %02X", pushedP)
	}
	if pushedP&FlagN == 0 {
		t.Fatalf("IRQ lost FlagN in pushed P: %02X", pushedP)
	}
}

// --- NMI ---

func TestNMI_FiresEvenWhenIRQMasked(t *testing.T) {
	c, r := newTestCPU([]byte{0xEA})
	installVector(r, VecNMI, 0x9100)
	c.setFlag(FlagI, true)
	c.TriggerNMI()
	c.Step()
	if c.PC != 0x9100 {
		t.Fatalf("NMI not taken with I set: PC=%04X want 9100", c.PC)
	}
}

func TestNMI_PushesPWithBClear(t *testing.T) {
	c, r := newTestCPU([]byte{0xEA})
	installVector(r, VecNMI, 0x9100)
	c.TriggerNMI()
	spBefore := c.SP
	c.Step()
	pushedP := c.Bus.Read(0x0100 | uint16(spBefore-2))
	if pushedP&FlagB != 0 {
		t.Fatalf("NMI pushed P with B set: %02X", pushedP)
	}
}

// --- Edge vs level semantics ---

// IRQ is level-triggered: while the line stays asserted the handler will be
// re-entered after RTI. Simulate one full IRQ -> RTI cycle and confirm
// re-entry.
func TestIRQ_LevelReentersAfterRTI(t *testing.T) {
	// $8000: NOP (target after RTI)
	// IRQ vector -> $9000: RTI
	c, r := newTestCPU([]byte{0xEA, 0xEA})
	installVector(r, VecIRQ, 0x9000)
	r.Write(0x9000, 0x40) // RTI
	c.setFlag(FlagI, false)
	c.AssertIRQ() // line held

	c.Step() // service IRQ -> PC=$9000, FlagI=1
	if c.PC != 0x9000 {
		t.Fatalf("first IRQ dispatch wrong: PC=%04X", c.PC)
	}
	c.Step() // execute RTI -> PC restored, FlagI restored to 0
	if c.PC != 0x8000 {
		t.Fatalf("RTI didn't restore PC: %04X want 8000", c.PC)
	}
	if c.hasFlag(FlagI) {
		t.Fatalf("RTI didn't restore FlagI=0")
	}
	// Line still held: next Step should re-enter the handler.
	c.Step()
	if c.PC != 0x9000 {
		t.Fatalf("level IRQ did not re-fire: PC=%04X want 9000", c.PC)
	}
}

// IRQ released before next boundary should not fire.
func TestIRQ_ReleaseStopsFiring(t *testing.T) {
	c, r := newTestCPU([]byte{0xEA, 0xEA})
	installVector(r, VecIRQ, 0x9000)
	c.setFlag(FlagI, false)
	c.AssertIRQ()
	c.ReleaseIRQ()
	c.Step()
	if c.PC != 0x8001 {
		t.Fatalf("released IRQ still fired: PC=%04X", c.PC)
	}
}

// NMI is edge-triggered: holding (or repeating TriggerNMI before RTI) must
// produce only ONE service. After RTI, a second TriggerNMI is required.
func TestNMI_EdgeFiresOnce(t *testing.T) {
	// $8000: NOP, NMI vector -> $9100: RTI
	c, r := newTestCPU([]byte{0xEA, 0xEA, 0xEA})
	installVector(r, VecNMI, 0x9100)
	r.Write(0x9100, 0x40) // RTI

	c.TriggerNMI()
	c.TriggerNMI() // coalesces — still one pending
	c.Step()       // service
	if c.PC != 0x9100 {
		t.Fatalf("NMI dispatch wrong: PC=%04X", c.PC)
	}
	c.Step() // RTI
	if c.PC != 0x8000 {
		t.Fatalf("RTI from NMI wrong: PC=%04X", c.PC)
	}
	// No new TriggerNMI -> next Step must execute NOP, not re-enter.
	c.Step()
	if c.PC != 0x8001 {
		t.Fatalf("NMI re-fired without new edge: PC=%04X want 8001", c.PC)
	}
}

// After RTI we can re-arm NMI with another TriggerNMI call.
func TestNMI_ReArmsAfterRTI(t *testing.T) {
	c, r := newTestCPU([]byte{0xEA, 0xEA})
	installVector(r, VecNMI, 0x9100)
	r.Write(0x9100, 0x40) // RTI

	c.TriggerNMI()
	c.Step() // dispatch
	c.Step() // RTI
	c.TriggerNMI()
	c.Step()
	if c.PC != 0x9100 {
		t.Fatalf("re-armed NMI did not fire: PC=%04X", c.PC)
	}
}

// --- Halted CPU wakes on interrupt ---

func TestIRQ_WakesHaltedCPU(t *testing.T) {
	// JMP self at $8000 (4C 00 80) — Step() marks Halted=true.
	c, r := newTestCPU([]byte{0x4C, 0x00, 0x80})
	installVector(r, VecIRQ, 0x9000)
	c.setFlag(FlagI, false)
	c.Step()
	if !c.Halted {
		t.Fatalf("self-JMP didn't halt CPU")
	}
	c.AssertIRQ()
	c.Step()
	if c.Halted {
		t.Fatalf("IRQ didn't wake halted CPU")
	}
	if c.PC != 0x9000 {
		t.Fatalf("IRQ from halted didn't vector: PC=%04X", c.PC)
	}
}
