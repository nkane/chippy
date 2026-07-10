package cpu

import "testing"

// irqCycle is one recorded 65816 bus cycle during interrupt entry: the 24-bit
// address, direction, and the access-type pins (busPins) the core asserted.
type irqCycle struct {
	addr  uint32
	write bool
	pins  byte
}

// irqTraceBus is a Bus24 that records every access plus the CPU's live busPins,
// so a test can assert the per-cycle bus sequence of a hardware interrupt — the
// Tom Harte 65816 corpus is opcodes-only and never exercises IRQ/NMI (#519).
type irqTraceBus struct {
	cpu   *CPU
	ram   map[uint32]byte
	trace []irqCycle
}

func (b *irqTraceBus) Read24(a uint32) byte {
	a &= 0xFFFFFF
	b.trace = append(b.trace, irqCycle{a, false, b.cpu.busPins})
	return b.ram[a]
}

func (b *irqTraceBus) Write24(a uint32, v byte) {
	a &= 0xFFFFFF
	b.trace = append(b.trace, irqCycle{a, true, b.cpu.busPins})
	b.ram[a] = v
}

func newIRQTrace() (*CPU, *irqTraceBus) {
	bus := &irqTraceBus{ram: map[uint32]byte{}}
	c := NewVariant(NewRAM(), VariantW65816)
	bus.cpu = c
	c.SetBus24(bus)
	return c, bus
}

func checkTrace(t *testing.T, got, want []irqCycle) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("trace length %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cycle %d = {$%06X w=%v pins=%02b}, want {$%06X w=%v pins=%02b}",
				i, got[i].addr, got[i].write, got[i].pins,
				want[i].addr, want[i].write, want[i].pins)
		}
	}
}

// Pin shorthands for the expected sequences.
const (
	pinsNone = byte(0)
	pinsData = pinVDA
	pinsVec  = pinVDA | pinVPB
)

// TestW65816_IRQBusTrace_Emulation pins the 7-cycle emulation-mode IRQ entry:
// two internal cycles at PBR:PC, three stack writes (PCH/PCL/P), two vector
// reads with VPB.
func TestW65816_IRQBusTrace_Emulation(t *testing.T) {
	c, bus := newIRQTrace()
	c.E = true
	c.PBR = 0
	c.PC = 0x8000
	c.setSP16(0x01FF)
	c.setFlag(FlagI, false) // I clear so the IRQ is taken
	bus.ram[0xFFFE] = 0x00
	bus.ram[0xFFFF] = 0x90 // IRQ vector -> $9000
	c.AssertIRQ()

	bus.trace = nil // discard any pre-interrupt setup reads
	c.Step()

	checkTrace(t, bus.trace, []irqCycle{
		{0x008000, false, pinsNone}, // internal 1 @ PBR:PC
		{0x008000, false, pinsNone}, // internal 2
		{0x0001FF, true, pinsData},  // push PCH
		{0x0001FE, true, pinsData},  // push PCL
		{0x0001FD, true, pinsData},  // push P
		{0x00FFFE, false, pinsVec},  // vector low
		{0x00FFFF, false, pinsVec},  // vector high
	})
	if c.PC != 0x9000 {
		t.Fatalf("PC=$%04X want $9000", c.PC)
	}
}

// TestW65816_AbortEmulation checks ABORT vectors through $FFF8 and pushes the
// aborted instruction's own PC (RTI re-runs it), even with I set.
func TestW65816_AbortEmulation(t *testing.T) {
	c, bus := newIRQTrace()
	c.E = true
	c.PBR = 0
	c.PC = 0x8000 // the instruction that gets aborted
	c.setSP16(0x01FF)
	c.setFlag(FlagI, true) // ABORT ignores I
	bus.ram[0xFFF8] = 0x00
	bus.ram[0xFFF9] = 0xA0 // ABORT vector -> $A000
	c.AssertAbort()

	c.Step()

	if c.PC != 0xA000 {
		t.Fatalf("ABORT vector: PC=$%04X want $A000", c.PC)
	}
	// Pushed PC is the aborted instruction ($8000), not the next one.
	if bus.ram[0x01FF] != 0x80 || bus.ram[0x01FE] != 0x00 {
		t.Fatalf("pushed PC = $%02X%02X want $8000", bus.ram[0x01FF], bus.ram[0x01FE])
	}
	if !c.hasFlag(FlagI) || c.hasFlag(FlagD) {
		t.Fatalf("after ABORT want I set, D clear; P=$%02X", c.P)
	}
}

// TestW65816_AbortNative confirms the native vector ($FFE8) + PBR push.
func TestW65816_AbortNative(t *testing.T) {
	c, bus := newIRQTrace()
	c.E = false
	c.PBR = 0x07
	c.PC = 0x8000
	c.setSP16(0x1FFF)
	bus.ram[0xFFE8] = 0x00
	bus.ram[0xFFE9] = 0xB0 // native ABORT vector -> $B000
	c.AssertAbort()

	c.Step()

	if c.PC != 0xB000 || c.PBR != 0 {
		t.Fatalf("ABORT native: PC=$%04X PBR=$%02X want $B000/$00", c.PC, c.PBR)
	}
	if bus.ram[0x1FFF] != 0x07 {
		t.Fatalf("native ABORT must push PBR, got $%02X want $07", bus.ram[0x1FFF])
	}
}

// TestW65816_AbortPriorityOverNMI checks ABORT is taken before a pending NMI.
func TestW65816_AbortPriorityOverNMI(t *testing.T) {
	c, bus := newIRQTrace()
	c.E = true
	c.PC = 0x8000
	c.setSP16(0x01FF)
	bus.ram[0xFFF8] = 0x00
	bus.ram[0xFFF9] = 0xA0 // ABORT -> $A000
	bus.ram[0xFFFA] = 0x00
	bus.ram[0xFFFB] = 0xC0 // NMI -> $C000
	c.AssertAbort()
	c.TriggerNMI()

	c.Step()

	if c.PC != 0xA000 {
		t.Fatalf("ABORT should win over NMI: PC=$%04X want $A000", c.PC)
	}
	if !c.abortPending && c.nmiPending {
		// NMI still latched, fires on the next step.
		c.Step()
		if c.PC != 0xC000 {
			t.Fatalf("NMI should fire after ABORT: PC=$%04X want $C000", c.PC)
		}
	}
}

// TestW65816_NMIBusTrace_Native pins the 8-cycle native-mode NMI entry: two
// internal cycles, the PBR push, three more stack writes, two vector reads.
func TestW65816_NMIBusTrace_Native(t *testing.T) {
	c, bus := newIRQTrace()
	c.E = false
	c.PBR = 0x12
	c.PC = 0x8000
	c.setSP16(0x1FFF)
	bus.ram[0xFFEA] = 0x34
	bus.ram[0xFFEB] = 0x56 // native NMI vector -> $5634
	c.TriggerNMI()

	bus.trace = nil
	c.Step()

	checkTrace(t, bus.trace, []irqCycle{
		{0x128000, false, pinsNone}, // internal 1 @ PBR:PC
		{0x128000, false, pinsNone}, // internal 2
		{0x001FFF, true, pinsData},  // push PBR
		{0x001FFE, true, pinsData},  // push PCH
		{0x001FFD, true, pinsData},  // push PCL
		{0x001FFC, true, pinsData},  // push P
		{0x00FFEA, false, pinsVec},  // vector low
		{0x00FFEB, false, pinsVec},  // vector high
	})
	if c.PC != 0x5634 || c.PBR != 0 {
		t.Fatalf("PC=$%04X PBR=$%02X want $5634/$00", c.PC, c.PBR)
	}
}
