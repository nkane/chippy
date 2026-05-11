package cpu_test

import (
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

func TestSnapshotRestore_RoundTrip(t *testing.T) {
	ram := cpu.NewRAM()
	// LDA #$42 ; STA $0200 ; JMP $8000
	ram.Load(0x8000, []byte{0xA9, 0x42, 0x8D, 0x00, 0x02, 0x4C, 0x00, 0x80})
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram)

	pre := c.Snapshot(ram)
	c.Step() // LDA #$42 -> A=$42, PC=$8002
	c.Step() // STA $0200 -> RAM[$0200]=$42, PC=$8005
	if c.A != 0x42 {
		t.Fatalf("post-step A want $42, got $%02X", c.A)
	}
	if ram.Read(0x0200) != 0x42 {
		t.Fatalf("post-step RAM[$0200] want $42, got $%02X", ram.Read(0x0200))
	}

	c.Restore(pre, ram)
	if c.PC != 0x8000 {
		t.Fatalf("restore PC want $8000, got $%04X", c.PC)
	}
	if c.A != 0 {
		t.Fatalf("restore A want $00, got $%02X", c.A)
	}
	if ram.Read(0x0200) != 0 {
		t.Fatalf("restore RAM[$0200] want $00, got $%02X", ram.Read(0x0200))
	}
}

func TestSnapshotRestore_BookkeepingFields(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	ram.Load(0x8000, []byte{0xEA, 0xEA}) // NOP NOP
	c := cpu.New(ram)

	c.AssertIRQ()
	c.TriggerNMI()
	s := c.Snapshot(ram)

	c.ReleaseIRQ() // clear IRQ line
	// Step to service the NMI; nmiPending flips false post-service.
	c.Step()
	if c.IRQAsserted() {
		t.Fatalf("between snapshot and step, ReleaseIRQ cleared the line; CPU should reflect that")
	}

	c.Restore(s, ram)
	if !c.IRQAsserted() {
		t.Fatalf("restore should rehydrate the IRQ line that was set at snapshot time")
	}
}

func TestSnapshot_RAMIsDeepCopy(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x0500, 0xAA)
	c := cpu.New(ram)

	s := c.Snapshot(ram)
	ram.Write(0x0500, 0xBB) // mutate after snapshot
	c.Restore(s, ram)
	if ram.Read(0x0500) != 0xAA {
		t.Fatalf("snapshot.RAM must be an independent copy: got $%02X, want $AA", ram.Read(0x0500))
	}
}
