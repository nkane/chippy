package cpu

// dma.go ports Mesen2's NesCpu::ProcessPendingDma (NesCpu.cpp:325-447)
// — the per-cycle OAMDMA + DMC sample-fetch state machine. Called from
// CPU.read at the top of every bus access; when needHalt is set, drains
// the full DMA window (halt + alignment dummies + 256 sprite read/write
// pairs ± DMC reads merged on getCycle slots) at the right CPU cycle
// parity. Sub-cycle ordering matters for cpu_interrupts_v2 tests 4-5
// (#376).
//
// Wiring is dormant until Phase 2B/2C of #376 flips OAMDMA.Write +
// DMC.maybeRefill from Stall(513) + StallStepper to SetNeedSpriteDma +
// SetNeedDmcDma. ProcessPendingDma already integrates here so the next
// phase can flip peripherals without further CPU changes.

// DMCFetcher is the slice of the APU the DMA loop needs: where the
// next sample byte comes from, and where to push the fetched value
// back. *apu.APU implements both methods (#376 Phase 1).
type DMCFetcher interface {
	GetDmcReadAddress() uint16
	SetDmcReadBuffer(v byte)
}

// DmaKind tags which sub-cycle of a DMA window a read belongs to. A
// host that implements DmaReadBus uses it to reproduce the 2A03's
// DMA-read open-bus / internal-register-read conflicts — chiefly the
// DmaDmcRead case landing on $4000-$401F (dmc_dma_during_read*, #481).
type DmaKind uint8

const (
	// DmaDummyRead is a halt-cycle or alignment dummy read (no data
	// consumed by the DMA, but still a real bus cycle the host may
	// want to latch into open bus).
	DmaDummyRead DmaKind = iota
	// DmaSpriteRead is an OAMDMA source-byte read (page $XX00).
	DmaSpriteRead
	// DmaDmcRead is the DMC sample fetch — the read whose landing on an
	// internal register produces the open-bus bus conflict.
	DmaDmcRead
)

// DmaReadBus is an optional Bus extension. When the CPU's Bus
// implements it, ProcessPendingDma routes its reads through ReadDma
// with a DmaKind tag, letting the host model the 2A03's DMA-read
// open-bus behavior (#481). Hosts that don't implement it get the
// plain Bus.Read path — byte-for-byte identical to pre-#481 behavior,
// and zero cost on non-NES variants. The CPU supplies only the tag;
// the open-bus latch and the internal-register conflict formula stay
// host-owned (validated in nessy against MesenCE).
type DmaReadBus interface {
	ReadDma(addr uint16, kind DmaKind) byte
}

// dmaRead issues one DMA-window bus read, routing through the host's
// DmaReadBus (tagged) when present, else the plain Bus.Read. dmaBus is
// a cached assertion (SetBus), so the 256-read sprite loop pays no
// per-read type-assert.
func (c *CPU) dmaRead(addr uint16, kind DmaKind) byte {
	if c.dmaBus != nil {
		return c.dmaBus.ReadDma(addr, kind)
	}
	if c.Bus != nil {
		return c.Bus.Read(addr)
	}
	return 0
}

// SetDMCFetcher wires the APU side of the DMC fetch — what address the
// DMA loop reads from and where to push the byte back. nil is fine for
// non-NES variants or pre-APU wiring; ProcessPendingDma silently
// drops DMC iterations in that case.
func (c *CPU) SetDMCFetcher(f DMCFetcher) { c.dmcFetcher = f }

// ProcessPendingDma drains a pending DMA window. Called from CPU.read
// just before the actual bus read. No-op when needHalt is clear.
// readAddress is the address the CPU was about to read (typically PC
// during opcode fetch) — used as the halt-cycle dummy read target and
// as the source for alignment dummy reads, matching Mesen's behavior.
func (c *CPU) ProcessPendingDma(readAddress uint16) {
	if !c.needHalt {
		return
	}
	c.needHalt = false

	// Halt cycle: dummy read at readAddress.
	c.dmaStartCycle()
	c.dmaRead(readAddress, DmaDummyRead)
	c.dmaEndCycle(true)

	var (
		spriteDmaCounter int
		spriteReadAddr   byte
		readValue        byte
	)

	for c.dmcDmaRunning || c.spriteDmaTransfer {
		// True even/odd CPU cycle, not c.Cycles alone: c.Cycles only
		// advances at the instruction boundary (exec.go), so mid-
		// instruction it is stale by instrCycles. Mesen's _cycleCount
		// ticks every cycle; getCycle must match its true parity or a
		// steal landing on an operand read (instrCycles>0) picks the
		// wrong alignment-cycle count, mis-sizing the 3-vs-4-cycle steal
		// and breaking dma_2007_read's phase drift (#493).
		getCycle := (c.Cycles+uint64(c.instrCycles))&1 == 0
		if getCycle {
			switch {
			case c.dmcDmaRunning && !c.needHalt && !c.needDummyRead:
				// DMC sample read — only after BOTH the halt cycle and the
				// extra dummy-read cycle have run (Mesen ordering).
				c.dmaProcessCycle()
				addr := uint16(0)
				if c.dmcFetcher != nil {
					addr = c.dmcFetcher.GetDmcReadAddress()
				}
				readValue = c.dmaRead(addr, DmaDmcRead)
				c.dmaEndCycle(true)
				c.dmcDmaRunning = false
				c.abortDmcDma = false
				if c.dmcFetcher != nil {
					c.dmcFetcher.SetDmcReadBuffer(readValue)
				}
			case c.spriteDmaTransfer:
				// OAMDMA sprite-byte read.
				c.dmaProcessCycle()
				addr := uint16(c.spriteDmaOffset)<<8 | uint16(spriteReadAddr)
				readValue = c.dmaRead(addr, DmaSpriteRead)
				c.dmaEndCycle(true)
				spriteReadAddr++
				spriteDmaCounter++
			default:
				// Alignment / halt dummy read.
				c.dmaProcessCycle()
				c.dmaRead(readAddress, DmaDummyRead)
				c.dmaEndCycle(true)
			}
		} else {
			// putCycle (odd parity).
			if c.spriteDmaTransfer && (spriteDmaCounter&1) == 1 {
				// Sprite write to $2004 (PPU OAMDATA).
				c.dmaProcessCycle()
				if c.Bus != nil {
					c.Bus.Write(0x2004, readValue)
				}
				c.dmaEndCycle(false)
				spriteDmaCounter++
				if spriteDmaCounter == 0x200 {
					c.spriteDmaTransfer = false
				}
			} else {
				// Alignment dummy read.
				c.dmaProcessCycle()
				c.dmaRead(readAddress, DmaDummyRead)
				c.dmaEndCycle(true)
			}
		}
	}
}

// dmaProcessCycle mirrors Mesen's processCycle lambda — abort/halt/
// dummy-read flag bookkeeping, then StartCpuCycle. Called at the top
// of each DMA iteration before the bus op.
func (c *CPU) dmaProcessCycle() {
	switch {
	case c.abortDmcDma:
		c.dmcDmaRunning = false
		c.abortDmcDma = false
		c.needDummyRead = false
		c.needHalt = false
	case c.needHalt:
		c.needHalt = false
	case c.needDummyRead:
		c.needDummyRead = false
	}
	c.dmaStartCycle()
}

// dmaStartCycle = Mesen StartCpuCycle(forRead=true) for the DMA path.
// Advances master clock, bumps cycle count, runs the PPU to the new
// deadline, then ticks the bus chain by one CPU cycle (APU advance).
// Read-shaped for the start half — DMA loop calls dmaEndCycle(false)
// on the write iteration to swap the post-shift sign.
func (c *CPU) dmaStartCycle() {
	c.masterClock += cpuStartReadShift
	c.Cycles++
	if c.ppuRunner != nil {
		c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
	}
	if c.busTicker != nil {
		c.busTicker.Tick(1)
	}
}

// dmaEndCycle = Mesen EndCpuCycle(forRead). Advances the post-half
// master clock, runs the PPU, then samples NMI/IRQ latches. forRead
// picks the read shift (+7) or write shift (+5) so a DMA write cycle
// gets the same +7 pre / +5 post mc split as a normal CPU write.
func (c *CPU) dmaEndCycle(forRead bool) {
	if forRead {
		c.masterClock += cpuEndReadShift
	} else {
		c.masterClock += cpuEndWriteShift
	}
	if c.ppuRunner != nil {
		c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
	}
	c.sampleNMI()
}
