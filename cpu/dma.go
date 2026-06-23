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
	if c.Bus != nil {
		c.Bus.Read(readAddress)
	}
	c.dmaEndCycle(true)

	var (
		spriteDmaCounter int
		spriteReadAddr   byte
		readValue        byte
	)

	for c.dmcDmaRunning || c.spriteDmaTransfer {
		getCycle := c.Cycles&1 == 0
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
				if c.Bus != nil {
					readValue = c.Bus.Read(addr)
				}
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
				if c.Bus != nil {
					readValue = c.Bus.Read(addr)
				}
				c.dmaEndCycle(true)
				spriteReadAddr++
				spriteDmaCounter++
			default:
				// Alignment / halt dummy read.
				c.dmaProcessCycle()
				if c.Bus != nil {
					c.Bus.Read(readAddress)
				}
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
				if c.Bus != nil {
					c.Bus.Read(readAddress)
				}
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
