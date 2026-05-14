package cpu

import "testing"

// fakeTicker is a Bus that also satisfies Ticker; counts calls and
// sums cycle deltas so tests can assert fan-out behavior.
type fakeTicker struct {
	ram   *RAM
	calls int
	total int
}

func (f *fakeTicker) Read(addr uint16) byte     { return f.ram.Read(addr) }
func (f *fakeTicker) Write(addr uint16, v byte) { f.ram.Write(addr, v) }
func (f *fakeTicker) Tick(cycles int) {
	f.calls++
	f.total += cycles
}

// CPU.Step() must invoke Bus.(Ticker).Tick after each instruction with
// the correct cycle delta — including branch +1 and CMOS BCD +1.
func TestTicker_FiresAfterEachInstructionWithCorrectDelta(t *testing.T) {
	ram := NewRAM()
	// LDA #$42 (2 cyc) ; NOP (2 cyc) ; NOP (2 cyc)
	ram.Load(0x8000, []byte{0xA9, 0x42, 0xEA, 0xEA})
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x80)
	bus := &fakeTicker{ram: ram}
	c := New(bus)

	c.Step() // LDA #$42 → 2 cyc
	c.Step() // NOP      → 2 cyc
	c.Step() // NOP      → 2 cyc

	if bus.calls != 3 {
		t.Fatalf("Tick should fire once per Step; got %d calls in 3 steps", bus.calls)
	}
	if bus.total != 6 {
		t.Fatalf("Tick total cycles want 6 (2+2+2); got %d", bus.total)
	}
}

// MMIO must fan out Tick() to every registered peripheral that
// implements Ticker, plus forward to the Inner bus if it's also a Ticker.
type tickerPeripheral struct {
	lo, hi uint16
	calls  int
}

func (p *tickerPeripheral) Range() (uint16, uint16)   { return p.lo, p.hi }
func (p *tickerPeripheral) Read(addr uint16) byte     { return 0 }
func (p *tickerPeripheral) Write(addr uint16, v byte) {}
func (p *tickerPeripheral) Tick(cycles int)           { p.calls++ }

// A peripheral that does NOT implement Ticker — confirms MMIO doesn't
// blindly call Tick on every peripheral.
type plainPeripheral struct{ lo, hi uint16 }

func (p *plainPeripheral) Range() (uint16, uint16)   { return p.lo, p.hi }
func (p *plainPeripheral) Read(addr uint16) byte     { return 0 }
func (p *plainPeripheral) Write(addr uint16, v byte) {}

func TestMMIO_TickFanOut(t *testing.T) {
	ram := NewRAM()
	mmio := NewMMIO(ram)
	ticking := &tickerPeripheral{lo: 0xF000, hi: 0xF00F}
	plain := &plainPeripheral{lo: 0xF010, hi: 0xF01F}
	_ = mmio.Register(ticking)
	_ = mmio.Register(plain)

	for i := 0; i < 5; i++ {
		mmio.Tick(7)
	}
	if ticking.calls != 5 {
		t.Fatalf("ticker peripheral want 5 Tick calls; got %d", ticking.calls)
	}
	// plain has no Tick method; compiler-time guaranteed not called. The
	// fan-out completing without panic is the assertion.
}
