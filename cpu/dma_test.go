package cpu

import "testing"

// dmaReadRec records one (addr, kind) pair seen by a DmaReadBus during
// a DMA window — the seam #481 adds so a host can model DMA-read open bus.
type dmaReadRec struct {
	addr uint16
	kind DmaKind
}

// recDmaBus is a 64K test memory that also implements DmaReadBus, so the
// DMA loop routes its reads through ReadDma (tagged) instead of Read.
type recDmaBus struct {
	ram   [0x10000]byte
	reads []dmaReadRec
}

func (b *recDmaBus) Read(a uint16) byte     { return b.ram[a] }
func (b *recDmaBus) Write(a uint16, v byte) { b.ram[a] = v }
func (b *recDmaBus) ReadDma(a uint16, k DmaKind) byte {
	b.reads = append(b.reads, dmaReadRec{a, k})
	return b.ram[a]
}

// plainBus is the same memory WITHOUT ReadDma — the fallback path.
type plainBus struct{ ram [0x10000]byte }

func (b *plainBus) Read(a uint16) byte     { return b.ram[a] }
func (b *plainBus) Write(a uint16, v byte) { b.ram[a] = v }

type fakeDmc struct {
	addr uint16
	buf  byte
}

func (f *fakeDmc) GetDmcReadAddress() uint16 { return f.addr }
func (f *fakeDmc) SetDmcReadBuffer(v byte)   { f.buf = v }

func (b *recDmaBus) count(k DmaKind) int {
	n := 0
	for _, r := range b.reads {
		if r.kind == k {
			n++
		}
	}
	return n
}

// A DMC sample fetch reaches the host bus tagged DmaDmcRead at the DMC's
// read address, and the fetched byte is routed back into the sample buffer.
func TestProcessPendingDma_DmcReadTagged(t *testing.T) {
	bus := &recDmaBus{}
	const dmcAddr, sample = 0x80F1, 0x5A
	bus.ram[dmcAddr] = sample

	c := NewVariant(bus, VariantNES)
	dmc := &fakeDmc{addr: dmcAddr}
	c.SetDMCFetcher(dmc)
	c.SetNeedDmcDma()
	c.ProcessPendingDma(0x0200)

	var got *dmaReadRec
	for i := range bus.reads {
		if bus.reads[i].kind == DmaDmcRead {
			got = &bus.reads[i]
		}
	}
	if got == nil {
		t.Fatalf("no DmaDmcRead recorded; reads=%+v", bus.reads)
	}
	if got.addr != dmcAddr {
		t.Errorf("DmaDmcRead addr = %#04x, want %#04x", got.addr, dmcAddr)
	}
	if dmc.buf != sample {
		t.Errorf("DMC sample buffer = %#02x, want %#02x", dmc.buf, sample)
	}
	if c.needHalt {
		t.Error("needHalt still set after DMA drained")
	}
	if c.dmcDmaRunning {
		t.Error("dmcDmaRunning still set after DMA drained")
	}
}

// OAMDMA issues 256 source reads tagged DmaSpriteRead across the source
// page, each followed by a $2004 write.
func TestProcessPendingDma_SpriteReadsTagged(t *testing.T) {
	bus := &recDmaBus{}
	const page = 0x03
	for i := 0; i < 256; i++ {
		bus.ram[page<<8|i] = byte(i)
	}

	c := NewVariant(bus, VariantNES)
	c.SetNeedSpriteDma(page)
	c.ProcessPendingDma(0x0200)

	if n := bus.count(DmaSpriteRead); n != 256 {
		t.Fatalf("DmaSpriteRead count = %d, want 256", n)
	}
	want := byte(0)
	for _, r := range bus.reads {
		if r.kind != DmaSpriteRead {
			continue
		}
		if r.addr != uint16(page)<<8|uint16(want) {
			t.Fatalf("sprite read %d addr = %#04x, want page %#02x offset %#02x", want, r.addr, page, want)
		}
		want++
	}
	if bus.ram[0x2004] != 0xFF {
		t.Errorf("last $2004 write = %#02x, want 0xFF", bus.ram[0x2004])
	}
	if c.spriteDmaTransfer {
		t.Error("spriteDmaTransfer still set after OAMDMA drained")
	}
}

// The halt cycle issues a dummy read at the CPU's pending read address,
// tagged DmaDummyRead.
func TestProcessPendingDma_HaltDummyTagged(t *testing.T) {
	bus := &recDmaBus{}
	c := NewVariant(bus, VariantNES)
	dmc := &fakeDmc{addr: 0x9000}
	c.SetDMCFetcher(dmc)
	c.SetNeedDmcDma()
	c.ProcessPendingDma(0x1234)

	if bus.count(DmaDummyRead) == 0 {
		t.Fatal("no DmaDummyRead recorded")
	}
	if bus.reads[0].kind != DmaDummyRead || bus.reads[0].addr != 0x1234 {
		t.Errorf("first read = %+v, want halt dummy at $1234", bus.reads[0])
	}
}

// dmcStealCycles runs a one-byte DMC DMA from a known cycle state and
// returns how many CPU cycles the steal consumed.
func dmcStealCycles(cyclesAtEntry uint64, instrCycles int) uint64 {
	bus := &recDmaBus{}
	bus.ram[0x9000] = 0x42
	c := NewVariant(bus, VariantNES)
	c.SetDMCFetcher(&fakeDmc{addr: 0x9000})
	c.Cycles = cyclesAtEntry
	c.instrCycles = instrCycles
	c.SetNeedDmcDma()
	before := c.Cycles
	c.ProcessPendingDma(0x8000)
	return c.Cycles - before
}

// The DMC-steal alignment is governed by the *true* CPU cycle parity
// (c.Cycles + instrCycles), not c.Cycles alone. c.Cycles is stale by
// instrCycles mid-instruction, so a steal on an operand read must still
// pick the right alignment — flipping instrCycles parity at a fixed
// c.Cycles toggles the steal between 3 and 4 cycles (#493).
func TestProcessPendingDma_StealParityUsesInstrCycles(t *testing.T) {
	even := dmcStealCycles(100, 0) // (100+0) even → no extra alignment
	odd := dmcStealCycles(100, 1)  // (100+1) odd  → one alignment cycle
	if even != 3 {
		t.Errorf("steal at even true-parity = %d, want 3", even)
	}
	if odd != 4 {
		t.Errorf("steal at odd true-parity = %d, want 4", odd)
	}
	if odd-even != 1 {
		t.Errorf("instrCycles parity does not affect steal length (even=%d odd=%d) — getCycle ignoring instrCycles?", even, odd)
	}
}

// tickerDmaBus is a recDmaBus that also implements Ticker, so NewVariant
// sets busTicker → nesCycle is true and the per-cycle interleave path
// (idle / busRead) runs — needed to exercise idle()'s DMA-halt poll.
type tickerDmaBus struct {
	recDmaBus
	ticks int
}

func (b *tickerDmaBus) Tick(n int) { b.ticks += n }

// idle() must drain a pending DMA halt on its own cycle, exactly like
// busRead — the 2A03 (and Mesen) poll ProcessPendingDma on every CPU
// cycle, including dummy/idle reads such as a taken branch's dummy read
// (branch() → c.idle(c.PC)). Regression for #493: without the poll, a
// halt armed going into an idle cycle was missed and only drained at the
// next real read, landing the DMC steal one cycle late (3-cycle steal vs
// the hardware 4) and freezing dma_2007_read's phase drift so its
// calibration loop never converged.
func TestIdle_DrainsPendingDmaHalt(t *testing.T) {
	bus := &tickerDmaBus{}
	bus.ram[0x9000] = 0x42

	c := NewVariant(bus, VariantNES)
	if !c.nesCycle {
		t.Fatal("nesCycle not set — test bus must implement Ticker")
	}
	c.SetDMCFetcher(&fakeDmc{addr: 0x9000})
	c.SetNeedDmcDma()

	// Drive a single idle (dummy-read) cycle with the halt armed.
	c.idle(0x8000)

	if c.needHalt {
		t.Error("idle() left needHalt set — DMA was not drained on the idle cycle")
	}
	if c.dmcDmaRunning {
		t.Error("idle() left dmcDmaRunning set — DMA was not drained")
	}
	if bus.count(DmaDmcRead) == 0 {
		t.Errorf("idle() did not issue the DMC sample read; reads=%+v", bus.reads)
	}
}

// A bus that does NOT implement DmaReadBus takes the plain Bus.Read
// fallback: same routed sample, same cycle count — byte-for-byte the
// pre-#481 behavior.
func TestProcessPendingDma_PlainBusFallback(t *testing.T) {
	const dmcAddr, sample = 0x80F1, 0x5A

	run := func(b Bus) (uint64, byte) {
		c := NewVariant(b, VariantNES)
		dmc := &fakeDmc{addr: dmcAddr}
		c.SetDMCFetcher(dmc)
		start := c.Cycles
		c.SetNeedDmcDma()
		c.ProcessPendingDma(0x0200)
		return c.Cycles - start, dmc.buf
	}

	plain := &plainBus{}
	plain.ram[dmcAddr] = sample
	tagged := &recDmaBus{}
	tagged.ram[dmcAddr] = sample

	pCyc, pBuf := run(plain)
	tCyc, tBuf := run(tagged)

	if pBuf != sample {
		t.Errorf("plain-bus DMC sample = %#02x, want %#02x", pBuf, sample)
	}
	if len(tagged.reads) == 0 {
		t.Error("DmaReadBus path recorded no reads")
	}
	if pCyc != tCyc {
		t.Errorf("cycle count differs: plain=%d tagged=%d", pCyc, tCyc)
	}
	if pBuf != tBuf {
		t.Errorf("routed sample differs: plain=%#02x tagged=%#02x", pBuf, tBuf)
	}
}
