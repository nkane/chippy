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
