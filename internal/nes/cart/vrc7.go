package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// VRC7 implements Konami's mapper 85. The cart packages a VRC4-shape
// PRG/CHR/IRQ surface with a Yamaha YM2413 (OPLL) FM-synth audio
// expansion. Lagrange Point is the only commercial NES VRC7 release.
//
// PRG layout: three switchable 8 KiB windows at $8000 / $A000 /
// $C000 plus a fixed last 8 KiB at $E000.
//
// CHR layout: eight switchable 1 KiB banks.
//
// Address-line routing: VRC7 picks the sub-register within each
// class via address bit 4 ($XXX0 vs $XXX1 in the conventional
// nesdev shorthand). One exception: the audio port pair lives at
// $9010 (register select) and $9030 (data write).
//
//	$8000           — PRG bank 0
//	$8010           — PRG bank 1
//	$9000           — PRG bank 2
//	$9010 / $9030   — audio register select / data
//	$A000 / $A010   — CHR banks 0 / 1
//	$B000 / $B010   — CHR banks 2 / 3
//	$C000 / $C010   — CHR banks 4 / 5
//	$D000 / $D010   — CHR banks 6 / 7
//	$E000           — mirroring + WRAM enable
//	$E010           — IRQ latch
//	$F000           — IRQ control
//	$F010           — IRQ ack
//
// IRQ behaviour matches VRC4 (CPU + scanline modes, 8-bit counter).
//
// Audio: this PR ships the cart side only. The OPLL FM synth is
// large enough to warrant its own follow-up issue (#315). Audio-
// register writes get forwarded through cart.VRC7AudioSink so a
// future apu.OPLL can drop in without touching the cart.
type VRC7 struct {
	prg      []byte
	chr      []byte
	chrIsRAM bool
	prgRAM   [0x2000]byte
	battery  bool

	prgBanks  [3]byte
	chrBanks  [8]byte
	mirroring nes.Mirroring
	wramOn    bool

	irqLatch          byte
	irqCounter        byte
	irqEnable         bool
	irqEnableAfterAck bool
	irqMode           byte
	irqPrescaler      int
	irqPending        bool
	irqSink           IRQSink

	audioSink VRC7AudioSink
}

const vrc7IRQSource = "vrc7"

// VRC7AudioSink is what the cart forwards the $9010 / $9030 audio
// register port pair to. apu.OPLL (future) will satisfy it.
type VRC7AudioSink interface {
	Write(addr uint16, v byte)
}

// NewVRC7 constructs a VRC7 cart.
func NewVRC7(rom *nes.ROM) (*VRC7, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(8*1024) != 0 {
		return nil, fmt.Errorf("vrc7: PRG must be a non-zero multiple of 8 KiB; got %d bytes", len(rom.PRG))
	}
	c := &VRC7{
		prg:       rom.PRG,
		battery:   rom.Battery,
		mirroring: rom.Mirroring,
		wramOn:    true,
	}
	switch {
	case len(rom.CHR) == 0:
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	case len(rom.CHR)%(1*1024) == 0:
		c.chr = rom.CHR
	default:
		return nil, fmt.Errorf("vrc7: CHR must be 0 or a multiple of 1 KiB; got %d bytes", len(rom.CHR))
	}
	return c, nil
}

// SetIRQSink wires the CPU's IRQ surface (optional).
func (c *VRC7) SetIRQSink(s IRQSink) { c.irqSink = s }

// SetAudioSink wires the OPLL FM synth (optional — silent when nil).
func (c *VRC7) SetAudioSink(s VRC7AudioSink) { c.audioSink = s }

// CPURead routes PRG-RAM + bank-switched ROM.
func (c *VRC7) CPURead(addr uint16) byte {
	switch {
	case addr < 0x6000:
		return 0
	case addr < 0x8000:
		if !c.wramOn {
			return 0
		}
		return c.prgRAM[addr-0x6000]
	}
	totalBanks := len(c.prg) / (8 * 1024)
	lastBank := totalBanks - 1
	var bank int
	switch {
	case addr < 0xA000:
		bank = int(c.prgBanks[0]) % totalBanks
	case addr < 0xC000:
		bank = int(c.prgBanks[1]) % totalBanks
	case addr < 0xE000:
		bank = int(c.prgBanks[2]) % totalBanks
	default:
		bank = lastBank
	}
	off := bank*8*1024 + int(addr&0x1FFF)
	return c.prg[off]
}

// CPUWrite handles PRG-RAM + register decode. The classes use
// address bit 4 to select the sub-register within their $X000 /
// $X010 pair; the audio port pair is a special-case at $9010 /
// $9030.
func (c *VRC7) CPUWrite(addr uint16, v byte) {
	switch {
	case addr < 0x6000:
		return
	case addr < 0x8000:
		if c.wramOn {
			c.prgRAM[addr-0x6000] = v
		}
		return
	}
	// Audio port pair sits at $9010 (register select) + $9030 (data).
	// They share $9000's class but skip the standard X0/X10 routing.
	switch addr & 0xF030 {
	case 0x9010:
		if c.audioSink != nil {
			c.audioSink.Write(0x9010, v)
		}
		return
	case 0x9030:
		if c.audioSink != nil {
			c.audioSink.Write(0x9030, v)
		}
		return
	}
	class := addr & 0xF000
	subHigh := addr&0x0010 != 0
	switch class {
	case 0x8000:
		if subHigh {
			c.prgBanks[1] = v & 0x3F
		} else {
			c.prgBanks[0] = v & 0x3F
		}
	case 0x9000:
		if !subHigh {
			c.prgBanks[2] = v & 0x3F
		}
		// $9010 / $9030 already handled above as audio.
	case 0xA000:
		if subHigh {
			c.chrBanks[1] = v
		} else {
			c.chrBanks[0] = v
		}
	case 0xB000:
		if subHigh {
			c.chrBanks[3] = v
		} else {
			c.chrBanks[2] = v
		}
	case 0xC000:
		if subHigh {
			c.chrBanks[5] = v
		} else {
			c.chrBanks[4] = v
		}
	case 0xD000:
		if subHigh {
			c.chrBanks[7] = v
		} else {
			c.chrBanks[6] = v
		}
	case 0xE000:
		if subHigh {
			c.irqLatch = v
		} else {
			c.applyMirroring(v)
		}
	case 0xF000:
		if subHigh {
			c.irqEnable = c.irqEnableAfterAck
			c.ackIRQ()
		} else {
			c.irqEnableAfterAck = v&0x01 != 0
			c.irqEnable = v&0x02 != 0
			c.irqMode = (v >> 2) & 0x01
			if c.irqEnable {
				c.irqCounter = c.irqLatch
				c.irqPrescaler = 341
			}
			c.ackIRQ()
		}
	}
}

func (c *VRC7) applyMirroring(v byte) {
	c.wramOn = v&0x80 != 0
	switch v & 0x03 {
	case 0:
		c.mirroring = nes.MirrorVertical
	case 1:
		c.mirroring = nes.MirrorHorizontal
	case 2:
		c.mirroring = nes.MirrorSingleLower
	case 3:
		c.mirroring = nes.MirrorSingleUpper
	}
}

func (c *VRC7) ackIRQ() {
	if c.irqPending {
		c.irqPending = false
		if c.irqSink != nil {
			c.irqSink.ClearIRQSource(vrc7IRQSource)
		}
	}
}

// PPURead serves CHR via the eight 1 KiB bank registers.
func (c *VRC7) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	bank := c.chrBanks[addr>>10]
	off := int(bank)*1024 + int(addr&0x03FF)
	return c.chr[off%len(c.chr)]
}

// PPUWrite is effective for CHR-RAM carts only.
func (c *VRC7) PPUWrite(addr uint16, v byte) {
	if !c.chrIsRAM || addr >= 0x2000 {
		return
	}
	bank := c.chrBanks[addr>>10]
	off := int(bank)*1024 + int(addr&0x03FF)
	c.chr[off%len(c.chr)] = v
}

// Mirroring + housekeeping.
func (c *VRC7) Mirroring() nes.Mirroring { return c.mirroring }
func (c *VRC7) BatteryBacked() bool      { return c.battery }
func (c *VRC7) PRGRAM() []byte           { return c.prgRAM[:] }

// Tick implements cpu.Ticker for the IRQ counter.
func (c *VRC7) Tick(cycles int) {
	if !c.irqEnable {
		return
	}
	for range cycles {
		if c.irqMode == 1 {
			c.tickCounter()
		} else {
			c.irqPrescaler--
			if c.irqPrescaler <= 0 {
				c.irqPrescaler += 341
				c.tickCounter()
			}
		}
	}
}

func (c *VRC7) tickCounter() {
	if c.irqCounter == 0xFF {
		c.irqCounter = c.irqLatch
		if !c.irqPending {
			c.irqPending = true
			if c.irqSink != nil {
				c.irqSink.AssertIRQSource(vrc7IRQSource)
			}
		}
	} else {
		c.irqCounter++
	}
}
