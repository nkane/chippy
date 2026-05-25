package cpu

import "testing"

// fakePeripheral is a counted-access stub used by the MMIO routing tests.
type fakePeripheral struct {
	lo, hi uint16
	reads  []uint16
	writes []struct {
		addr uint16
		v    byte
	}
	readVal byte
}

func (f *fakePeripheral) Range() (uint16, uint16) { return f.lo, f.hi }
func (f *fakePeripheral) Read(addr uint16) byte {
	f.reads = append(f.reads, addr)
	return f.readVal
}
func (f *fakePeripheral) Write(addr uint16, v byte) {
	f.writes = append(f.writes, struct {
		addr uint16
		v    byte
	}{addr, v})
}

func TestMMIOFallthroughToRAM(t *testing.T) {
	ram := NewRAM()
	m := NewMMIO(ram)

	m.Write(0x0200, 0x42)
	if got := m.Read(0x0200); got != 0x42 {
		t.Fatalf("want 0x42, got 0x%02X", got)
	}
	if ram.Data[0x0200] != 0x42 {
		t.Fatalf("underlying RAM not written")
	}
}

func TestMMIODispatchesReadsAndWrites(t *testing.T) {
	ram := NewRAM()
	m := NewMMIO(ram)
	p := &fakePeripheral{lo: 0xF001, hi: 0xF001, readVal: 0xAB}
	if err := m.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}

	m.Write(0xF001, 0x55)
	if len(p.writes) != 1 || p.writes[0].addr != 0xF001 || p.writes[0].v != 0x55 {
		t.Fatalf("peripheral did not see write: %+v", p.writes)
	}
	if ram.Data[0xF001] != 0x00 {
		t.Fatalf("RAM should not be written when peripheral claims address; got 0x%02X", ram.Data[0xF001])
	}

	got := m.Read(0xF001)
	if got != 0xAB {
		t.Fatalf("peripheral read mismatch: got 0x%02X", got)
	}
	if len(p.reads) != 1 || p.reads[0] != 0xF001 {
		t.Fatalf("peripheral did not see read: %+v", p.reads)
	}
}

func TestMMIOOnlyRoutesInsideRange(t *testing.T) {
	ram := NewRAM()
	m := NewMMIO(ram)
	p := &fakePeripheral{lo: 0xF000, hi: 0xF00F, readVal: 0x11}
	_ = m.Register(p)

	// Address just below range falls through to RAM.
	m.Write(0xEFFF, 0x77)
	if ram.Data[0xEFFF] != 0x77 {
		t.Fatalf("address below peripheral range should hit RAM")
	}
	if len(p.writes) != 0 {
		t.Fatalf("peripheral should not see writes below its range")
	}

	// Address just above range falls through to RAM.
	m.Write(0xF010, 0x88)
	if ram.Data[0xF010] != 0x88 {
		t.Fatalf("address above peripheral range should hit RAM")
	}

	// Boundary addresses are inclusive on both ends.
	m.Write(0xF000, 0x01)
	m.Write(0xF00F, 0x02)
	if len(p.writes) != 2 {
		t.Fatalf("peripheral should see both boundary writes, got %d", len(p.writes))
	}
}

func TestMMIOOverlapRejected(t *testing.T) {
	m := NewMMIO(NewRAM())
	a := &fakePeripheral{lo: 0xF000, hi: 0xF00F}
	b := &fakePeripheral{lo: 0xF008, hi: 0xF017}

	if err := m.Register(a); err != nil {
		t.Fatalf("first register failed: %v", err)
	}
	if err := m.Register(b); err == nil {
		t.Fatalf("overlapping register should fail")
	}
}

func TestMMIOInvalidRange(t *testing.T) {
	m := NewMMIO(NewRAM())
	if err := m.Register(&fakePeripheral{lo: 0xF010, hi: 0xF000}); err == nil {
		t.Fatalf("inverted range should fail")
	}
}

func TestMMIOFirstPeripheralWins(t *testing.T) {
	// Non-overlapping siblings: each is routed to independently.
	m := NewMMIO(NewRAM())
	a := &fakePeripheral{lo: 0xF001, hi: 0xF001, readVal: 0xAA}
	b := &fakePeripheral{lo: 0xF004, hi: 0xF005, readVal: 0xBB}
	_ = m.Register(a)
	_ = m.Register(b)

	if v := m.Read(0xF001); v != 0xAA {
		t.Fatalf("want 0xAA from a, got 0x%02X", v)
	}
	if v := m.Read(0xF004); v != 0xBB {
		t.Fatalf("want 0xBB from b, got 0x%02X", v)
	}
	if v := m.Read(0xF005); v != 0xBB {
		t.Fatalf("want 0xBB from b, got 0x%02X", v)
	}
}
