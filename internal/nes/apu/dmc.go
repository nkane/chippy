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
}

// DMCBus is the slice of the CPU bus the DMC reads sample bytes
// from. Any cpu.Bus / *cpu.MMIO / cpu.WBus satisfies this.
type DMCBus interface {
	Read(addr uint16) byte
}

// DMCStaller is the cpu.Stall(int) hook from #204. *cpu.CPU
// satisfies it directly.
type DMCStaller interface {
	Stall(cycles int)
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
func (d *dmcChannel) setEnabled(on bool) {
	d.enabled = on
	if on {
		if d.bytesRemaining == 0 {
			d.currentAddr = d.sampleAddrBase
			d.bytesRemaining = d.sampleLenBase
		}
	} else {
		d.bytesRemaining = 0
	}
}

// tickTimer drops the period timer by one CPU cycle. When it
// underflows it clocks one shift bit and (if the sample buffer is
// empty + bytes-remaining > 0) requests a DMA refill via the
// caller-supplied stall + bus.
func (d *dmcChannel) tickTimer(bus DMCBus, staller DMCStaller, irqSink IRQSink) {
	period := dmcRateLUT[d.rateIdx]
	if d.timer == 0 {
		d.timer = period
		d.clockShift()
		d.maybeRefill(bus, staller, irqSink)
		return
	}
	d.timer--
}

// clockShift moves one bit out of the shift register and adjusts
// the output level. Real silicon's bit-counter loads from
// sampleBuffer when bitsRemaining hits zero; v0.3 keeps the model
// simple by loading at the top of the unit (every 8 bits).
func (d *dmcChannel) clockShift() {
	if d.silenced || d.bitsRemaining == 0 {
		return
	}
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
	d.bitsRemaining--
}

// maybeRefill restocks the shift register from the sample buffer
// when the 8-bit unit is exhausted. If the buffer is empty and the
// sample pointer still has bytes to fetch, DMA one byte from the
// CPU bus (stealing 4 cycles via cpu.Stall per nesdev). On final-
// byte exhaustion, loop or assert DMC IRQ.
func (d *dmcChannel) maybeRefill(bus DMCBus, staller DMCStaller, irqSink IRQSink) {
	if d.bitsRemaining != 0 {
		return
	}
	if d.bufferEmpty {
		d.silenced = true
		return
	}
	d.silenced = false
	d.shiftRegister = d.sampleBuffer
	d.bufferEmpty = true
	d.bitsRemaining = 8

	// Now refill the sample buffer from CPU memory if possible.
	if d.bytesRemaining > 0 && bus != nil {
		if staller != nil {
			staller.Stall(4)
		}
		d.sampleBuffer = bus.Read(d.currentAddr)
		d.bufferEmpty = false
		if d.currentAddr == 0xFFFF {
			d.currentAddr = 0x8000 // wrap per nesdev
		} else {
			d.currentAddr++
		}
		d.bytesRemaining--
		if d.bytesRemaining == 0 {
			if d.loop {
				d.currentAddr = d.sampleAddrBase
				d.bytesRemaining = d.sampleLenBase
			} else if d.irqEnable {
				d.irqPending = true
				if irqSink != nil {
					irqSink.AssertIRQSource(dmcIRQSource)
				}
			}
		}
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
