package cpu

import "testing"

// CPU.Stall queues cycles; the very next Step() drains the whole
// counter as one block — bus ticker fires once with the full delta,
// Cycles advances, no opcode executes. Issue #204 / OAMDMA.
func TestStall_DrainsOnNextStep(t *testing.T) {
	ram := NewRAM()
	// LDA #$00 ; LDA #$00 — two trivial 2-cycle instructions.
	ram.Load(0x8000, []byte{0xA9, 0x00, 0xA9, 0x00})
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	bus := &fakeTicker{ram: ram}
	c := New(bus)

	// First Step: normal opcode + 2-cyc tick.
	got := c.Step()
	if got != 2 {
		t.Fatalf("step 1 cycles = %d; want 2", got)
	}
	pcBeforeStall := c.PC

	// Queue a stall, then Step. Expect the drain to consume all 513
	// cycles in one shot, fire one Tick, leave PC untouched.
	c.Stall(513)
	got = c.Step()
	if got != 513 {
		t.Fatalf("stall step cycles = %d; want 513", got)
	}
	if c.PC != pcBeforeStall {
		t.Fatalf("stall step advanced PC: $%04X → $%04X", pcBeforeStall, c.PC)
	}
	if c.pendingStall != 0 {
		t.Fatalf("pendingStall = %d after drain; want 0", c.pendingStall)
	}
	if bus.calls != 2 {
		t.Fatalf("ticker calls = %d; want 2 (1 opcode + 1 stall drain)", bus.calls)
	}
	if bus.total != 2+513 {
		t.Fatalf("ticker total = %d; want %d", bus.total, 2+513)
	}

	// Next Step picks up where we left off — the queue is empty so the
	// opcode at PC executes normally.
	got = c.Step()
	if got != 2 {
		t.Fatalf("post-stall step cycles = %d; want 2", got)
	}
	if c.PC != pcBeforeStall+2 {
		t.Fatalf("post-stall PC = $%04X; want $%04X", c.PC, pcBeforeStall+2)
	}
}

// Stall calls accumulate — two queues drain together.
func TestStall_Accumulates(t *testing.T) {
	ram := NewRAM()
	ram.Load(0x8000, []byte{0xEA}) // NOP
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	bus := &fakeTicker{ram: ram}
	c := New(bus)

	c.Stall(100)
	c.Stall(50)
	if c.pendingStall != 150 {
		t.Fatalf("pendingStall = %d after 100+50; want 150", c.pendingStall)
	}
	got := c.Step()
	if got != 150 {
		t.Fatalf("drain cycles = %d; want 150", got)
	}
}

// Negative or zero Stall is a no-op — caller bugs shouldn't corrupt
// the counter.
func TestStall_RejectsNonPositive(t *testing.T) {
	c := New(NewRAM())
	c.Stall(0)
	c.Stall(-5)
	if c.pendingStall != 0 {
		t.Fatalf("pendingStall = %d after no-op calls; want 0", c.pendingStall)
	}
}

// STP-halt traps the CPU at the top of Step() before any other
// state machine runs. A queued stall stays queued forever; only
// Reset clears the latch. Models real CMOS silicon behavior.
func TestStall_BlockedByStpHalt(t *testing.T) {
	ram := NewRAM()
	ram.Load(0x8000, []byte{0xEA}) // NOP filler
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	bus := &fakeTicker{ram: ram}
	c := NewVariant(bus, VariantCMOS65C02)

	// Force STP halt directly via the unexported latch (the test
	// lives in package cpu, so this is fine).
	c.Halted = true
	c.stoppedBySTP = true

	c.Stall(513)
	got := c.Step()
	if got != 0 {
		t.Fatalf("STP-halted Step returned %d cycles; want 0 (stall must not drain)", got)
	}
	if c.pendingStall != 513 {
		t.Fatalf("pendingStall = %d after STP-blocked Step; want 513 still queued", c.pendingStall)
	}

	// Reset clears the latch; next Step then drains the stall queue.
	c.Reset()
	if c.stoppedBySTP {
		t.Fatalf("Reset did not clear stoppedBySTP")
	}
	// Reset also reinitializes pendingStall implicitly via the queue
	// staying intact — Reset doesn't touch pendingStall, by design,
	// so the stall is still owed. Drain it.
	got = c.Step()
	if got != 513 {
		t.Fatalf("post-Reset stall drain = %d; want 513", got)
	}
}

// WAI-halt (CMOS-only): Step() returns 0 once Halted is set with no
// interrupt pending. A queued stall placed before the WAI must still
// drain — pendingStall is checked *before* the Halted gate. After
// drain, subsequent Steps return 0 until an interrupt wakes the CPU.
func TestStall_DrainsBeforeWaiHaltGate(t *testing.T) {
	ram := NewRAM()
	ram.Load(0x8000, []byte{0xEA})
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	bus := &fakeTicker{ram: ram}
	c := NewVariant(bus, VariantCMOS65C02)

	// Simulate WAI: halted, NOT stoppedBySTP.
	c.Halted = true

	c.Stall(513)
	got := c.Step()
	if got != 513 {
		t.Fatalf("WAI-halted Step stall drain = %d; want 513", got)
	}
	// Subsequent Steps return 0 until an interrupt wakes the CPU.
	if got = c.Step(); got != 0 {
		t.Fatalf("post-drain WAI-halted Step = %d; want 0", got)
	}
	// Tick fired exactly once with the 513 delta.
	if bus.calls != 1 || bus.total != 513 {
		t.Fatalf("ticker fired %d times totaling %d; want 1 call of 513", bus.calls, bus.total)
	}
}

// Pending NMI preempts a queued stall — the next Step services the
// interrupt, the stall waits one boundary, then drains.
func TestStall_NMIPreempts(t *testing.T) {
	ram := NewRAM()
	ram.Load(0x8000, []byte{0xEA}) // NOP at reset PC
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	// NMI vector points somewhere safe so the service push+jump works.
	ram.Write(VecNMI, 0x00)
	ram.Write(VecNMI+1, 0x90)
	ram.Load(0x9000, []byte{0xEA, 0x40}) // NOP ; RTI
	c := New(ram)

	c.Stall(513)
	c.TriggerNMI()
	got := c.Step()
	// NMI service is 7 cycles per cpu.go; stall must still be pending.
	if got != 7 {
		t.Fatalf("NMI service cycles = %d; want 7", got)
	}
	if c.pendingStall != 513 {
		t.Fatalf("pendingStall = %d after NMI preempt; want 513 still queued", c.pendingStall)
	}
	// Next Step drains the stall.
	got = c.Step()
	if got != 513 {
		t.Fatalf("post-NMI stall drain = %d; want 513", got)
	}
}
