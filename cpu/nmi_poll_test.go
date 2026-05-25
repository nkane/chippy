package cpu

import "testing"

// newNMITestCPU loads `prog` at $8000, points the reset vector there and
// the NMI vector at $9000, and returns a CPU of the given variant.
func newNMITestCPU(prog []byte, v Variant) (*CPU, *RAM) {
	r := NewRAM()
	r.Load(0x8000, prog)
	r.Write(VecReset, 0x00)
	r.Write(VecReset+1, 0x80)
	r.Write(VecNMI, 0x00)
	r.Write(VecNMI+1, 0x90)
	return NewVariant(r, v), r
}

// NMOS services a pending NMI at the next instruction boundary: the very
// next Step jumps to the handler without executing the queued opcode.
func TestNMI_NMOS_ServicesImmediately(t *testing.T) {
	c, _ := newNMITestCPU([]byte{0xEA, 0xEA}, VariantNMOS) // NOP, NOP
	c.TriggerNMI()
	c.Step()
	if c.PC != 0x9000 {
		t.Fatalf("NMOS NMI not serviced on first Step: PC=%04X want 9000", c.PC)
	}
}

// NES samples interrupts before an instruction's final cycle (#342), so
// an NMI pending at a boundary is polled *during* the next instruction
// and serviced one instruction later — the queued opcode runs first.
func TestNMI_NES_PollDelaysOneInstruction(t *testing.T) {
	c, _ := newNMITestCPU([]byte{0xEA, 0xEA}, VariantNES) // NOP, NOP
	c.TriggerNMI()
	c.Step() // runs the first NOP; the poll latches nmiDue
	if c.PC != 0x8001 {
		t.Fatalf("NES serviced NMI too early: PC=%04X want 8001 (one NOP run)", c.PC)
	}
	c.Step() // now the NMI dispatches
	if c.PC != 0x9000 {
		t.Fatalf("NES NMI not serviced on second Step: PC=%04X want 9000", c.PC)
	}
}
