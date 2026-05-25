package cpu

import "testing"

// peekablePeripheral implements both Peripheral and Peeker; Peek
// returns the same bytes regardless of how many times Read is
// called (Read counts invocations so the test can prove Peek
// avoided it).
type peekablePeripheral struct {
	lo, hi uint16
	value  byte
	reads  int
	writes int
}

func (p *peekablePeripheral) Range() (uint16, uint16) { return p.lo, p.hi }
func (p *peekablePeripheral) Read(addr uint16) byte {
	p.reads++
	return p.value
}
func (p *peekablePeripheral) Write(addr uint16, v byte) { p.writes++; p.value = v }
func (p *peekablePeripheral) Peek(addr uint16) byte     { return p.value }

// readOnlyPeripheral implements Peripheral but NOT Peeker. Memory
// inspectors hitting its range fall back to MMIO.Inner.
type readOnlyPeripheral struct {
	lo, hi uint16
	reads  int
}

func (p *readOnlyPeripheral) Range() (uint16, uint16)   { return p.lo, p.hi }
func (p *readOnlyPeripheral) Read(addr uint16) byte     { p.reads++; return 0xAB }
func (p *readOnlyPeripheral) Write(addr uint16, v byte) {}

// MMIO.Peek prefers a peripheral's Peek over Read so inspectors
// don't trigger Read side effects.
func TestMMIOPeek_RoutesThroughPeeker(t *testing.T) {
	ram := NewRAM()
	mmio := NewMMIO(ram)
	p := &peekablePeripheral{lo: 0xF000, hi: 0xF00F, value: 0x42}
	if err := mmio.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got := mmio.Peek(0xF000)
	if got != 0x42 {
		t.Errorf("Peek = $%02X; want $42", got)
	}
	if p.reads != 0 {
		t.Errorf("Peek triggered Read %d times; want 0", p.reads)
	}
}

// Peripheral without Peeker → fall through to Inner.Read at that
// address (returns 0 for unmapped RAM). Does NOT invoke the
// peripheral's Read.
func TestMMIOPeek_FallsThroughOnNonPeeker(t *testing.T) {
	ram := NewRAM()
	ram.Load(0xF000, []byte{0x99})
	mmio := NewMMIO(ram)
	p := &readOnlyPeripheral{lo: 0xF000, hi: 0xF00F}
	if err := mmio.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got := mmio.Peek(0xF000)
	if got != 0x99 {
		t.Errorf("Peek = $%02X; want $99 (Inner.Read for peripheral that doesn't implement Peeker)", got)
	}
	if p.reads != 0 {
		t.Errorf("Peek triggered Read on non-Peeker peripheral %d times; want 0", p.reads)
	}
}

// Addresses outside any peripheral range fall through to Inner.Read.
func TestMMIOPeek_UnmappedAddrReadsInner(t *testing.T) {
	ram := NewRAM()
	ram.Load(0x0200, []byte{0x77})
	mmio := NewMMIO(ram)

	if got := mmio.Peek(0x0200); got != 0x77 {
		t.Errorf("Peek($0200) = $%02X; want $77", got)
	}
}
