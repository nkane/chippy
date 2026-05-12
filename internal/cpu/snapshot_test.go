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

	// CoW snapshot protocol (issue #66): regs captured before step,
	// pages claimed after via ram.TakeShadow().
	ram.EnableShadow()
	pre := c.Snapshot(ram)
	ram.ResetShadow()
	c.Step() // LDA #$42 -> A=$42, PC=$8002
	c.Step() // STA $0200 -> RAM[$0200]=$42, PC=$8005
	pre.Pages = ram.TakeShadow()
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
	ram.EnableShadow()
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

// CoW page tracking: a tight 1000-iteration loop that writes to two RAM
// pages should produce 1000 snapshots whose total before-image volume
// is well under 1 MiB. Validates issue #66's acceptance criterion.
func TestSnapshotCoW_TightLoopRingFitsInMemoryBudget(t *testing.T) {
	ram := cpu.NewRAM()
	ram.EnableShadow()
	// LDX #$00              ; $8000 A2 00
	// loop: STX $0200       ; $8002 8E 00 02
	//       INX             ; $8005 E8
	//       STX $0300       ; $8006 8E 00 03
	//       JMP loop        ; $8009 4C 02 80
	ram.Load(0x8000, []byte{
		0xA2, 0x00,
		0x8E, 0x00, 0x02,
		0xE8,
		0x8E, 0x00, 0x03,
		0x4C, 0x02, 0x80,
	})
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram)

	const N = 1000
	snaps := make([]cpu.Snapshot, 0, N)
	totalBytes := 0
	for i := 0; i < N; i++ {
		snap := c.Snapshot(ram)
		ram.ResetShadow()
		c.Step()
		snap.Pages = ram.TakeShadow()
		totalBytes += len(snap.Pages) * 256
		snaps = append(snaps, snap)
	}

	if totalBytes >= 1<<20 {
		t.Fatalf("CoW snapshots should fit in <1 MiB for 1000-step tight loop; got %d bytes", totalBytes)
	}

	// Rewinding all the way back must restore the CPU to its pre-loop state.
	for i := len(snaps) - 1; i >= 0; i-- {
		c.Restore(snaps[i], ram)
	}
	if c.PC != 0x8000 {
		t.Fatalf("full rewind want PC $8000; got $%04X", c.PC)
	}
	if c.X != 0 {
		t.Fatalf("full rewind want X=0; got $%02X", c.X)
	}
}

// Cross-page writes (STA $00FF,Y where the target wraps into page $01)
// must capture both pages' before-images independently.
func TestSnapshotCoW_TwoPagesInOneStep(t *testing.T) {
	ram := cpu.NewRAM()
	ram.EnableShadow()
	ram.Write(0x00FF, 0xAA)
	ram.Write(0x0100, 0xBB)

	ram.ResetShadow()
	ram.Write(0x00FF, 0x11)
	ram.Write(0x0100, 0x22)
	delta := ram.TakeShadow()

	if len(delta) != 2 {
		t.Fatalf("two-page mutation want 2 entries in delta; got %d", len(delta))
	}
	if delta[0x00][0xFF] != 0xAA {
		t.Fatalf("page 0 before-image want $AA; got $%02X", delta[0x00][0xFF])
	}
	if delta[0x01][0x00] != 0xBB {
		t.Fatalf("page 1 before-image want $BB; got $%02X", delta[0x01][0x00])
	}
}

// Without EnableShadow(), Write must NOT track anything. Existing
// non-rewind callers (Klaus harness, decimal tests) rely on zero
// overhead.
func TestSnapshotCoW_ShadowDisabledByDefault(t *testing.T) {
	ram := cpu.NewRAM()
	if ram.ShadowEnabled() {
		t.Fatalf("shadow should be disabled by default")
	}
	ram.Write(0x1234, 0xFF)
	if d := ram.TakeShadow(); d != nil {
		t.Fatalf("TakeShadow on disabled RAM should return nil; got %v", d)
	}
}

func TestSnapshot_PageDeltaIsIndependent(t *testing.T) {
	ram := cpu.NewRAM()
	ram.EnableShadow()
	ram.Write(0x0500, 0xAA)
	c := cpu.New(ram)

	// Capture pre-step regs, then mutate, then claim the delta. Restore
	// should roll back to $AA and the captured before-image must not
	// alias the live RAM.
	s := c.Snapshot(ram)
	ram.ResetShadow()
	ram.Write(0x0500, 0xBB)
	s.Pages = ram.TakeShadow()

	// Mutate live RAM again AFTER claiming the delta. The captured page
	// image must not change.
	ram.Write(0x0500, 0xCC)
	c.Restore(s, ram)
	if ram.Read(0x0500) != 0xAA {
		t.Fatalf("delta page must round-trip cleanly: got $%02X, want $AA", ram.Read(0x0500))
	}
}
