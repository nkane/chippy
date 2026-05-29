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

// fakeDMCStaller records CPU-side DMA-fetch signals. The real CPU
// drains the bus-steal inside ProcessPendingDma (#376); tests stand
// in by counting SetNeedDmcDma calls.
type fakeDMCStaller struct {
	needCalls int
}

func (s *fakeDMCStaller) SetNeedDmcDma() { s.needCalls++ }

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

// Timer expiry that empties the sample buffer signals the CPU via
// SetNeedDmcDma — the actual bus.Read runs inside ProcessPendingDma
// on the next opcode fetch (#376 Phase 2C). Verify the signal fires.
func TestDMC_TimerExpirySignalsFetch(t *testing.T) {
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0xC3, 0x5A, 0xFF}}
	staller := &fakeDMCStaller{}
	setupDMC(a, bus, staller, nil)
	a.Write(0x4013, 0x01) // length = 17 bytes
	s.Write(0x4015, 0x10)

	// Pump enough cycles to drain one shift unit (8 bits) and trigger
	// a refill request. rateIdx 0 = 428 CPU cycles per shift.
	a.Tick(5000)

	if staller.needCalls == 0 {
		t.Fatalf("DMC didn't request a CPU-side DMA fetch (SetNeedDmcDma calls = 0)")
	}
	if !a.DmcFetchPending() {
		t.Errorf("DmcFetchPending = false; want true after refill request")
	}
	if got := a.GetDmcReadAddress(); got != 0xC000 {
		t.Errorf("GetDmcReadAddress = $%04X; want $C000 (sample base)", got)
	}
}

// SetDmcReadBuffer hands a fetched byte back to the channel,
// advances the sample pointer, decrements bytesRemaining, and
// clears the fetch-pending flag so the next timer expiry can
// queue another fetch.
func TestDMC_SetDmcReadBufferAdvancesPointer(t *testing.T) {
	a := New()
	s := NewStatus(a)
	bus := &fakeDMCBus{bytes: []byte{0xAA, 0xBB}}
	staller := &fakeDMCStaller{}
	setupDMC(a, bus, staller, nil)
	a.Write(0x4013, 0x01) // length = 17
	s.Write(0x4015, 0x10)
	a.Tick(5000) // drive a refill request

	pre := a.dmc.bytesRemaining
	a.SetDmcReadBuffer(0x77)

	if a.dmc.sampleBuffer != 0x77 {
		t.Errorf("sampleBuffer = $%02X; want $77", a.dmc.sampleBuffer)
	}
	if a.dmc.bufferEmpty {
		t.Errorf("bufferEmpty = true; want false after fill")
	}
	if a.dmc.bytesRemaining != pre-1 {
		t.Errorf("bytesRemaining = %d; want %d", a.dmc.bytesRemaining, pre-1)
	}
	if a.dmc.currentAddr != 0xC001 {
		t.Errorf("currentAddr = $%04X; want $C001 (advanced)", a.dmc.currentAddr)
	}
	if a.DmcFetchPending() {
		t.Errorf("DmcFetchPending = true; want false after SetDmcReadBuffer")
	}
}

// fakeIRQSink reuses the helper from irq_test.go (same package).
// Loop disabled + IRQ enabled fires the DMC IRQ when bytes-
// remaining reaches zero. Drive via SetDmcReadBuffer (the path the
// CPU's ProcessPendingDma takes).
func TestDMC_IRQAssertsOnExhaustion(t *testing.T) {
	a := New()
	s := NewStatus(a)
	staller := &fakeDMCStaller{}
	sink := newFakeSink()
	a.SetDMCBus(&fakeDMCBus{bytes: []byte{0xFF}}, staller)
	a.SetIRQSink(sink)
	a.Write(0x4010, 0x80) // IRQ enabled, loop off, rate idx 0
	a.Write(0x4012, 0x00) // base $C000
	a.Write(0x4013, 0x00) // length 1 byte
	s.Write(0x4015, 0x10)

	// Feed one byte through the CPU-side hook to exhaust the 1-byte
	// sample.
	a.SetDmcReadBuffer(0xFF)

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
	staller := &fakeDMCStaller{}
	sink := newFakeSink()
	a.SetDMCBus(&fakeDMCBus{bytes: []byte{0xAA}}, staller)
	a.SetIRQSink(sink)
	a.Write(0x4010, 0x40) // loop on, IRQ off, rate idx 0
	a.Write(0x4012, 0x00)
	a.Write(0x4013, 0x00) // length 1
	s.Write(0x4015, 0x10)

	// One CPU-side fetch exhausts the 1-byte sample; loop should reload.
	a.SetDmcReadBuffer(0xAA)

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
