package apu

import "testing"

// fakeDMCBus serves a flat byte slice as the CPU memory image the
// DMC reads from. Address arithmetic is mod len so a small slice
// can stand in for the full sample region.
type fakeDMCBus struct {
	bytes []byte
	reads []uint16
}

func (b *fakeDMCBus) Read(addr uint16) byte {
	b.reads = append(b.reads, addr)
	if len(b.bytes) == 0 {
		return 0
	}
	return b.bytes[int(addr)%len(b.bytes)]
}

type fakeDMCStaller struct {
	stalled int
	pending int // simulated OAMDMA debt for contention tests
}

func (s *fakeDMCStaller) Stall(c int)       { s.stalled += c }
func (s *fakeDMCStaller) PendingStall() int { return s.pending }

// helper: configures DMC with a tiny sample (one byte) starting at
// $C000, rate idx 0 (fastest), no loop / no IRQ.
func setupDMC(a *APU, bus *fakeDMCBus, staller *fakeDMCStaller, irq IRQSink) {
	a.SetDMCBus(bus, staller)
	a.SetIRQSink(irq)
	a.Write(0x4010, 0x00) // IRQ off, loop off, rate idx 0
	a.Write(0x4012, 0x00) // base $C000
	a.Write(0x4013, 0x00) // length = 1 byte
}

// Enable via $4015 bit 4 + bytes-remaining > 0 reads back as bit 4
// set in the status register.
func TestDMC_EnableLoadsByteCounter(t *testing.T) {
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0xAA}}
	staller := &fakeDMCStaller{}
	setupDMC(a, bus, staller, nil)

	s.Write(0x4015, 0x10)
	if a.dmc.bytesRemaining == 0 {
		t.Fatalf("$4015 bit 4 didn't reload bytesRemaining")
	}
	if got := s.Read(0x4015); got&0x10 == 0 {
		t.Errorf("$4015 bit 4 not set; got $%02X", got)
	}

	// Disable drains bytesRemaining.
	s.Write(0x4015, 0x00)
	if a.dmc.bytesRemaining != 0 {
		t.Errorf("$4015 disable didn't clear bytesRemaining")
	}
}

// $4011 direct output write moves the DAC level immediately + is
// independent of channel enable.
func TestDMC_DirectOutputWriteOverridesLevel(t *testing.T) {
	a := New()
	// $4015 NOT touched → channel disabled.
	a.Write(0x4011, 0x55)
	if a.dmc.output != 0x55 {
		t.Errorf("$4011 direct write didn't latch output level: got $%02X", a.dmc.output)
	}
}

// Sample fetch on timer expiry reads from the configured base
// address + charges 4 stall cycles per byte. Pump the timer just
// long enough to drive the first DMA cycle.
func TestDMC_FetchReadsBaseAddressAndStalls(t *testing.T) {
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0xC3, 0x5A, 0xFF}}
	staller := &fakeDMCStaller{}
	setupDMC(a, bus, staller, nil)
	a.Write(0x4013, 0x01) // length = 17 bytes (so multiple fetches happen)
	s.Write(0x4015, 0x10)

	// Pump enough cycles to drain one shift-register unit (8 bits)
	// + trigger one refill. rateIdx 0 = 428 CPU cycles per shift; 8
	// shifts ≈ 3424 cycles. Add headroom.
	// Drive in chunks so we can drain pending DMC fetches between Ticks
	// (real CPU calls StepDMCFetch from its stall drain; tests here
	// don't have a CPU, so we drain manually).
	for i := 0; i < 50; i++ {
		a.Tick(100)
		for !a.StepDMCFetch() {
		}
	}

	if len(bus.reads) == 0 {
		t.Fatalf("DMC didn't read any sample bytes")
	}
	if first := bus.reads[0]; first != 0xC000 {
		t.Errorf("first DMC read = $%04X; want $C000", first)
	}
	if staller.stalled < 4 {
		t.Errorf("DMC didn't charge stall cycles; got %d, want >= 4", staller.stalled)
	}
}

// DMC/OAMDMA contention (#300): when the staller already has
// pending OAMDMA debt, each DMC fetch pays 2 extra cycles for the
// bus-alignment penalty. With no pending debt the standard 4-cycle
// stall stands.
func TestDMC_OAMDMAContentionAdds2Cycles(t *testing.T) {
	// Baseline: fetch with no OAMDMA pending → 4-cycle stall per fetch.
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0x00}}
	staller := &fakeDMCStaller{}
	setupDMC(a, bus, staller, nil)
	a.Write(0x4013, 0x00) // 1-byte sample
	s.Write(0x4015, 0x10)
	for i := 0; i < 50; i++ {
		a.Tick(100)
		for !a.StepDMCFetch() {
		}
	}
	baseline := staller.stalled
	if baseline%4 != 0 {
		t.Fatalf("baseline stall %d not a multiple of 4 — fetch path drifted", baseline)
	}

	// Contention: same setup but staller reports pending OAMDMA
	// debt at fetch time → each fetch charges 6 instead of 4.
	a2 := New()
	s2 := NewStatus(a2)
	bus2 := &fakeDMCBus{bytes: []byte{0x00}}
	staller2 := &fakeDMCStaller{pending: 100} // any non-zero
	setupDMC(a2, bus2, staller2, nil)
	a2.Write(0x4013, 0x00)
	s2.Write(0x4015, 0x10)
	for i := 0; i < 50; i++ {
		a2.Tick(100)
		for !a2.StepDMCFetch() {
		}
	}
	contended := staller2.stalled
	if baseline == 0 || contended == 0 {
		t.Fatalf("no fetches happened (baseline=%d contended=%d)", baseline, contended)
	}
	// Same number of fetches between the two runs → contended
	// stall should be baseline + 2*fetches.
	fetches := baseline / 4
	want := contended
	if got := baseline + 2*fetches; got != want {
		t.Errorf("contended stall = %d; baseline=%d fetches=%d want %d", contended, baseline, fetches, got)
	}
}

// fakeIRQSink reuses the helper from irq_test.go (same package).
// Loop disabled + IRQ enabled fires the DMC IRQ when bytes-
// remaining reaches zero.
func TestDMC_IRQAssertsOnExhaustion(t *testing.T) {
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0xFF}}
	staller := &fakeDMCStaller{}
	sink := newFakeSink()
	a.SetDMCBus(bus, staller)
	a.SetIRQSink(sink)
	a.Write(0x4010, 0x80) // IRQ enabled, loop off, rate idx 0
	a.Write(0x4012, 0x00) // base $C000
	a.Write(0x4013, 0x00) // length 1 byte
	s.Write(0x4015, 0x10)

	// Run enough cycles to fetch + exhaust the single-byte sample.
	for i := 0; i < 1000; i++ {
		a.Tick(100)
		for !a.StepDMCFetch() {
		}
	}

	if !a.dmc.irqPending {
		t.Errorf("DMC IRQ flag not pending after sample exhaustion")
	}
	if sink.asserted[dmcIRQSource] == 0 {
		t.Errorf("DMC didn't AssertIRQSource on exhaustion")
	}
}

// Loop bit re-triggers fetch on exhaustion — bytesRemaining
// reloads to the base length instead of firing an IRQ.
func TestDMC_LoopReloadsOnExhaustion(t *testing.T) {
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0xAA}}
	staller := &fakeDMCStaller{}
	sink := newFakeSink()
	a.SetDMCBus(bus, staller)
	a.SetIRQSink(sink)
	a.Write(0x4010, 0x40) // loop on, IRQ off, rate idx 0
	a.Write(0x4012, 0x00)
	a.Write(0x4013, 0x00) // length 1
	s.Write(0x4015, 0x10)

	a.Tick(100_000)

	if a.dmc.bytesRemaining == 0 {
		t.Errorf("loop should reload bytesRemaining; got 0")
	}
	if sink.asserted[dmcIRQSource] != 0 {
		t.Errorf("loop mode shouldn't fire DMC IRQ; asserted %d times", sink.asserted[dmcIRQSource])
	}
}

// $4015 read clears the pending DMC IRQ flag + drops the sink
// assertion.
func TestDMC_Read4015ClearsDMCIRQ(t *testing.T) {
	a := New()
	s := NewStatus(a)
	sink := newFakeSink()
	a.SetIRQSink(sink)
	// Force IRQ-pending state directly (skip the full DMA dance).
	a.dmc.irqPending = true
	sink.AssertIRQSource(dmcIRQSource)

	v := s.Read(0x4015)
	if v&0x80 == 0 {
		t.Errorf("$4015 read = $%02X; want bit 7 set", v)
	}
	if a.dmc.irqPending {
		t.Errorf("$4015 read didn't clear DMC IRQ flag")
	}
	if sink.cleared[dmcIRQSource] == 0 {
		t.Errorf("$4015 read didn't ClearIRQSource(dmc)")
	}
}

// $4010 write with bit 7 = 0 clears any pending DMC IRQ.
func TestDMC_WriteReg0ClearsIRQ(t *testing.T) {
	a := New()
	sink := newFakeSink()
	a.SetIRQSink(sink)
	a.dmc.irqPending = true
	sink.AssertIRQSource(dmcIRQSource)

	a.Write(0x4010, 0x00) // bit 7 cleared
	if a.dmc.irqPending {
		t.Errorf("$4010 bit 7 = 0 didn't clear DMC IRQ")
	}
	if sink.cleared[dmcIRQSource] == 0 {
		t.Errorf("$4010 bit 7 = 0 didn't drop sink assertion")
	}
}
