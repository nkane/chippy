package cpu

import "testing"

// WAI ($CB) halts the CPU. Until an IRQ or NMI arrives, Step() returns 0
// cycles and PC stays put.
func TestCMOS_WAI_HaltsCPU(t *testing.T) {
	// WAI ; LDA #$42 — LDA must NOT execute until something wakes us.
	c, _ := newCMOS([]byte{0xCB, 0xA9, 0x42})
	c.Step() // WAI
	if !c.Halted {
		t.Fatalf("WAI should set Halted")
	}
	if c.A != 0 {
		t.Fatalf("pre-wake A=%02X want 0", c.A)
	}
	// Spin a few times — no progress while halted with no IRQ asserted.
	for i := 0; i < 5; i++ {
		if got := c.Step(); got != 0 {
			t.Fatalf("Step while WAI-halted returned %d cycles", got)
		}
	}
	if c.A != 0 || c.PC != 0x8001 {
		t.Fatalf("post-WAI-halt drift: A=%02X PC=%04X", c.A, c.PC)
	}
}

// WAI wakes on IRQ even when FlagI is set. In that case the handler is
// not invoked — execution falls straight through to the instruction
// after WAI on the same Step.
func TestCMOS_WAI_WakesOnMaskedIRQ(t *testing.T) {
	// SEI ; WAI ; LDA #$42
	c, r := newCMOS([]byte{0x78, 0xCB, 0xA9, 0x42})
	// Vector points elsewhere — handler must NOT run.
	r.Write(VecIRQ, 0x00)
	r.Write(VecIRQ+1, 0x90)
	r.Load(0x9000, []byte{0xA9, 0x55, 0x40}) // LDA #$55 ; RTI (sentinel)
	c.Step()                                 // SEI (FlagI set)
	c.Step()                                 // WAI
	if !c.Halted {
		t.Fatalf("WAI did not halt")
	}
	c.AssertIRQ()
	c.Step() // un-halt and run LDA #$42 (no handler dispatch — I masked)
	if c.Halted {
		t.Fatalf("WAI should wake on masked IRQ")
	}
	if c.PC != 0x8004 {
		t.Fatalf("after WAI wake PC=%04X want 8004 (post-LDA)", c.PC)
	}
	if c.A != 0x42 {
		t.Fatalf("WAI wake should fall through to LDA #$42; A=%02X want 42", c.A)
	}
	if c.PC == 0x9000 {
		t.Fatalf("masked IRQ should NOT have dispatched the handler")
	}
}

// WAI on unmasked IRQ: line asserts, FlagI clear, service runs on the
// step that follows the wake.
func TestCMOS_WAI_WakesAndServicesIRQ(t *testing.T) {
	// CLI ; WAI ; LDA #$11   (handler at $9000 sets A=$55)
	c, r := newCMOS([]byte{0x58, 0xCB, 0xA9, 0x11})
	r.Write(VecIRQ, 0x00)
	r.Write(VecIRQ+1, 0x90)
	r.Load(0x9000, []byte{0xA9, 0x55, 0x40}) // LDA #$55 ; RTI
	c.Step()                                 // CLI
	c.Step()                                 // WAI
	if !c.Halted {
		t.Fatalf("WAI did not halt")
	}
	c.AssertIRQ()
	c.Step() // service IRQ (7 cycles, jumps to $9000)
	if c.PC != 0x9000 {
		t.Fatalf("IRQ service: PC=%04X want 9000", c.PC)
	}
	c.ReleaseIRQ()
	c.Step() // LDA #$55
	c.Step() // RTI
	if c.A != 0x55 {
		t.Fatalf("handler did not run: A=%02X want 55", c.A)
	}
	if c.PC != 0x8002 {
		t.Fatalf("RTI did not return to WAI+1: PC=%04X want 8002", c.PC)
	}
}

// STP ($DB) halts the CPU permanently. Interrupts are ignored; only
// Reset wakes it.
func TestCMOS_STP_HaltsUntilReset(t *testing.T) {
	c, r := newCMOS([]byte{0xDB, 0xA9, 0x42})
	r.Write(VecIRQ, 0x00)
	r.Write(VecIRQ+1, 0x90)
	c.Step() // STP
	if !c.Halted {
		t.Fatalf("STP should set Halted")
	}
	// NMI must NOT wake an STP-halted CPU.
	c.TriggerNMI()
	for i := 0; i < 5; i++ {
		c.Step()
	}
	if c.PC != 0x8001 {
		t.Fatalf("STP-halted CPU advanced under NMI: PC=%04X want 8001", c.PC)
	}
	// IRQ must NOT wake either.
	c.AssertIRQ()
	for i := 0; i < 5; i++ {
		c.Step()
	}
	if c.PC != 0x8001 {
		t.Fatalf("STP-halted CPU advanced under IRQ: PC=%04X want 8001", c.PC)
	}
	// Reset clears the latch.
	c.Reset()
	if c.Halted {
		t.Fatalf("Reset should clear STP halt")
	}
}

// IZP $FF zero-page wrap: pointer is read at $FF/$00, not $FF/$100.
func TestCMOS_IZP_WrapsZeroPage(t *testing.T) {
	// LDA ($FF). Pointer high byte must come from $00, not $0100.
	c, r := newCMOS([]byte{0xB2, 0xFF})
	r.Write(0x00FF, 0x34)
	r.Write(0x0000, 0x12) // high byte via wrap
	r.Write(0x0100, 0xAA) // would-be-wrong source if wrap absent
	r.Write(0x1234, 0x77)
	c.Step()
	if c.A != 0x77 {
		t.Fatalf("IZP wrap: A=%02X want 77 (pointer should read $FF/$00)", c.A)
	}
}

// PHP pushes P with B=1 + U=1; PLA observes both. (Software-initiated
// push always sets B; only BRK is documented as B-clear-in-pushed-byte,
// but BRK actually pushes B=1 too — the "B is clear" myth is about IRQ/NMI.)
func TestCPU_PHP_PushesBAndU(t *testing.T) {
	// PHP ; PLA — A should hold the pushed P byte.
	c, _ := newTestCPU([]byte{0x08, 0x68})
	c.P = FlagC // start with C set, U/B will be added on push
	c.Step()    // PHP
	c.Step()    // PLA
	if c.A&FlagB == 0 {
		t.Fatalf("PHP pushed P without B set: A=%02X", c.A)
	}
	if c.A&FlagU == 0 {
		t.Fatalf("PHP pushed P without U set: A=%02X", c.A)
	}
	if c.A&FlagC == 0 {
		t.Fatalf("PHP lost C flag: A=%02X", c.A)
	}
}

// IRQ pushes P with B=0 (distinguishes IRQ-pushed P from BRK/PHP).
func TestCPU_IRQ_PushesBClear(t *testing.T) {
	// CLI ; NOP ; (IRQ fires) — handler reads pushed P off stack.
	// Handler at $9000: PLA ; STA $0500 ; RTI — top of stack at entry is P.
	// Wait — IRQ pushes PC(2) then P. So at handler entry SP points at P.
	c, r := newTestCPU([]byte{0x58, 0xEA, 0xEA})
	r.Write(VecIRQ, 0x00)
	r.Write(VecIRQ+1, 0x90)
	// PLA pops top-of-stack (the P byte), STA $0500, RTI.
	r.Load(0x9000, []byte{0x68, 0x8D, 0x00, 0x05, 0x40})
	c.Step()      // CLI
	c.AssertIRQ() // line stays asserted
	c.Step()      // services IRQ (NOP at $8001 not yet executed)
	c.ReleaseIRQ()
	c.Step() // PLA — pulls P off stack into A
	if c.A&FlagB != 0 {
		t.Fatalf("IRQ pushed P with B set: $%02X (B should be clear)", c.A)
	}
	if c.A&FlagU == 0 {
		t.Fatalf("IRQ pushed P without U set: $%02X (U should be 1)", c.A)
	}
}

// CMOS interrupt clears D on entry; the original D is preserved on the
// stack so RTI restores the user's pre-interrupt decimal-mode setting.
func TestCMOS_RTI_RestoresPreInterruptD(t *testing.T) {
	// SED ; CLI ; NOP ; (IRQ fires) — handler asserts D=0 mid-handler,
	// then RTIs. After RTI, D should be 1 again.
	c, r := newCMOS([]byte{0xF8, 0x58, 0xEA, 0xEA})
	r.Write(VecIRQ, 0x00)
	r.Write(VecIRQ+1, 0x90)
	// Handler: PHP ; PLA ; STA $0501 ; RTI — capture D-state inside handler.
	r.Load(0x9000, []byte{0x08, 0x68, 0x8D, 0x01, 0x05, 0x40})
	c.Step()      // SED
	c.Step()      // CLI
	c.AssertIRQ() // line stays asserted
	c.Step()      // services IRQ
	if c.hasFlag(FlagD) {
		t.Fatalf("CMOS interrupt entry did not clear D")
	}
	c.ReleaseIRQ()
	c.Step() // PHP
	c.Step() // PLA -> A = handler P (D should be clear here)
	if c.A&FlagD != 0 {
		t.Fatalf("handler ran with D=1: $%02X (CMOS should clear D)", c.A)
	}
	c.Step() // STA $0501
	c.Step() // RTI
	if !c.hasFlag(FlagD) {
		t.Fatalf("RTI did not restore pre-interrupt D=1: P=$%02X", c.P)
	}
}
