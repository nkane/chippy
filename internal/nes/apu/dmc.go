package apu

// dmcChannel models the NES delta-modulation channel ($4010-$4013).
// Plays 1-bit delta samples fetched from CPU memory. Two side
// effects vs the other channels: the DMA byte fetch steals CPU
// cycles (via the cpu.Stall hook from #204) and sample-exhaustion
// can assert an IRQ on the named "apu-dmc" source (#247).
//
// Output is a 7-bit DAC level (0..127). Each timer expiry shifts
// one bit out of the sample buffer; bit=1 nudges the level up by
// 2 (clamped at 125), bit=0 nudges it down by 2 (clamped at 2).
// $4011 writes directly override the level — useful for "audio
// thump" sound effects.
type dmcChannel struct {
	enabled bool

	// $4010 fields.
	irqEnable bool
	loop      bool
	rateIdx   byte // 0..15 → dmcRateLUT

	// $4011 — direct 7-bit output level.
	output byte // 0..127

	// $4012 / $4013 latched reload values + the live cursor /
	// counter that drives the next DMA fetch.
	sampleAddrBase uint16 // $C000 + (v * 64)
	sampleLenBase  uint16 // (v * 16) + 1
	currentAddr    uint16
	bytesRemaining uint16

	// Per-bit shift state. sampleBuffer holds the most recent
	// fetched byte; bufferEmpty flags whether the next timer
	// expiry should request a refill. bitsRemaining counts how
	// many bits of the shifter still need clocking out of the
	// current 8-bit unit.
	sampleBuffer  byte
	bufferEmpty   bool
	shiftRegister byte
	bitsRemaining byte
	silenced      bool // true when no sample is currently playing

	// Period timer ticks every CPU cycle; underflow advances one
	// shift bit (and triggers refill / output update).
	timer uint16

	// IRQ pending flag. Set on bytes-remaining-reaches-zero with
	// irqEnable + no loop. $4015 read clears.
	irqPending bool

	// fetchPending flags that a sample-buffer refill is needed. The
	// CPU drains the actual fetch inside ProcessPendingDma (#376
	// Phase 2C): SetNeedDmcDma sets the CPU-side dmcDmaRunning flag
	// and on the next opcode fetch read the CPU issues bus.Read at
	// APU.GetDmcReadAddress + pushes the byte back via
	// APU.SetDmcReadBuffer. The flag stays set until SetDmcReadBuffer
	// fires so a second timer expiry inside the same DMA window
	// doesn't queue a duplicate fetch.
	fetchPending bool
}

// DMCBus is the slice of the CPU bus the DMC reads sample bytes
// from. Any cpu.Bus / *cpu.MMIO / cpu.WBus satisfies this.
type DMCBus interface {
	Read(addr uint16) byte
}

// DMCStaller is the CPU-side hook the DMC uses to flag a pending
// sample-byte fetch. *cpu.CPU satisfies it via SetNeedDmcDma. The
// older Stall + PendingStall surface retired in #376 Phase 2C —
// cpu.ProcessPendingDma handles the cycle-by-cycle DMA window
// (including DMC-during-OAMDMA contention) so the channel just
// signals intent.
type DMCStaller interface {
	SetNeedDmcDma()
}

const dmcIRQSource = "apu-dmc"

// writeReg0 handles $4010 — bit 7 IRQ enable, bit 6 loop, bits 0-3
// rate index. Clearing IRQ-enable also clears any pending DMC IRQ
// flag (per nesdev).
func (d *dmcChannel) writeReg0(v byte) {
	d.irqEnable = v&0x80 != 0
	d.loop = v&0x40 != 0
	d.rateIdx = v & 0x0F
	if !d.irqEnable {
		d.irqPending = false
	}
}

// writeReg1 handles $4011 — bits 0-6 set the output DAC level
// directly. Bit 7 is ignored (zero on real silicon).
func (d *dmcChannel) writeReg1(v byte) {
	d.output = v & 0x7F
}

// writeReg2 handles $4012 — sample address: $C000 + (v * 64).
func (d *dmcChannel) writeReg2(v byte) {
	d.sampleAddrBase = 0xC000 + (uint16(v) << 6)
}

// writeReg3 handles $4013 — sample length: (v * 16) + 1 bytes.
func (d *dmcChannel) writeReg3(v byte) {
	d.sampleLenBase = (uint16(v) << 4) + 1
}

// setEnabled mirrors $4015 bit-4. Enabling reloads the cursor +
// length IF bytes-remaining is currently zero (per nesdev — avoids
// restarting a sample mid-play). Disabling clears bytes-remaining
// + the IRQ flag; output level + sample buffer survive.
//
// When enabling with the sample buffer empty + bytesRemaining > 0
// after reload, immediately flag a DMA fetch via the CPU sink.
// Mesen2 DeltaModulationChannel::SetEnabled delays this by 2-3 CPU
// cycles depending on cycle parity; chippy fires inline since the
// CPU's ProcessPendingDma drains on the next bus read regardless
// and Blargg apu_test 7-dmc_basics test 19 cares only that the
// fetch happens before the user-visible $4015 poll a few dozen
// cycles later (#318).
func (d *dmcChannel) setEnabled(on bool, staller DMCStaller) {
	d.enabled = on
	if on {
		if d.bytesRemaining == 0 {
			d.currentAddr = d.sampleAddrBase
			d.bytesRemaining = d.sampleLenBase
		}
		// Schedule a fetch immediately if the buffer is empty +
		// bytes are pending (Mesen StartDmcTransfer condition). The
		// existing leftover byte in the buffer plays out first if
		// bufferEmpty is false; no fetch scheduled until that byte
		// has been clocked through.
		if d.bytesRemaining > 0 && d.bufferEmpty && staller != nil && !d.fetchPending {
			d.fetchPending = true
			staller.SetNeedDmcDma()
		}
	} else {
		d.bytesRemaining = 0
	}
}

// tickTimer drops the period timer by one CPU cycle. When the
// timer underflows the clock fires (Mesen2 DeltaModulationChannel
// ::Run/Clock). bitsRemaining=8 invariant (init in APU.New) +
// always-shift semantics in clock() mean 8 clocks per byte exactly,
// no extra "reload-only" cycle — matches Mesen's 17-byte sample at
// rate 0 = 17*8*428 cycles total.
func (d *dmcChannel) tickTimer(_ DMCBus, staller DMCStaller, _ IRQSink) {
	if d.timer == 0 {
		// Reload to period-1 so fire-to-fire = period CPU cycles
		// exactly, matching Mesen2 ApuTimer::Run (which advances by
		// `_timer + 1` per fire with period stored as rate-1).
		d.timer = dmcRateLUT[d.rateIdx] - 1
		d.clock(staller)
		return
	}
	d.timer--
}

// clock is one DMC output unit tick. Mirrors Mesen2 Delta
// ModulationChannel::Run inner body:
//
//  1. If not silenced: emit a bit (adjust output level ±2, shift
//     shift-register one place).
//  2. Always decrement bitsRemaining.
//  3. If bitsRemaining == 0: reset to 8 + reload shifter from
//     sample buffer (or silence if buffer is empty).
//  4. Schedule a DMA fetch if the buffer is empty + bytes remain.
//
// The fetch-schedule check runs every clock, not just at unit
// boundaries, so the memory reader is responsive to mid-byte
// disable/re-enable sequences without burning extra timer cycles.
func (d *dmcChannel) clock(staller DMCStaller) {
	if !d.silenced {
		if d.shiftRegister&1 == 1 {
			if d.output <= 125 {
				d.output += 2
			}
		} else {
			if d.output >= 2 {
				d.output -= 2
			}
		}
		d.shiftRegister >>= 1
	}
	d.bitsRemaining--
	if d.bitsRemaining == 0 {
		d.bitsRemaining = 8
		if d.bufferEmpty {
			d.silenced = true
		} else {
			d.silenced = false
			d.shiftRegister = d.sampleBuffer
			d.bufferEmpty = true
		}
	}
	if d.bufferEmpty && d.bytesRemaining > 0 && staller != nil && !d.fetchPending {
		d.fetchPending = true
		staller.SetNeedDmcDma()
	}
}

// mixerOutput returns the current output level (0-127) for the APU
// mixer. Independent of enable / silence — $4011 direct writes
// should still drive the DAC even with the channel disabled, per
// nesdev's "audio thump" pattern.
func (d *dmcChannel) mixerOutput() byte {
	return d.output
}

// clearIRQ acks the DMC IRQ — invoked by APU.Read on $4015.
func (d *dmcChannel) clearIRQ(irqSink IRQSink) {
	d.irqPending = false
	if irqSink != nil {
		irqSink.ClearIRQSource(dmcIRQSource)
	}
}
