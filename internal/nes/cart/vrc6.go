package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// VRC6 implements Konami's mapper 24 / 26 — VRC6a / VRC6b. Adds a
// dedicated 3-channel audio expansion (2 pulse + 1 sawtooth) on
// top of a VRC4-style PRG/CHR/IRQ surface.
//
// PRG layout:
//
//	$8000-$BFFF — switchable 16 KiB (command 0)
//	$C000-$DFFF — switchable 8 KiB (command 4)
//	$E000-$FFFF — fixed last 8 KiB
//
// CHR layout: eight switchable 1 KiB banks via commands 5-12.
//
// Register classes (per CPU address bits 12-15):
//
//	$8000 — PRG bank 0 (16 KiB)
//	$9000-$9002 — pulse 1 audio
//	$A000-$A002 — pulse 2 audio
//	$B000-$B002 — sawtooth audio
//	$B003       — banking + mirroring control
//	$C000 — PRG bank 1 (8 KiB)
//	$D000-$D003 — CHR banks 0-3
//	$E000-$E003 — CHR banks 4-7
//	$F000-$F003 — IRQ
//
// Mapper 24 = VRC6a (A0,A1 routing). Mapper 26 = VRC6b (A1,A0
// swapped). subBits() picks the variant.
//
// The audio chip is exposed via the Sink hook so cmd/nessy can
// wire it into the APU mixer at construction time, identical to
// how Sunsoft 5B is wired for FME-7.
type VRC6 struct {
	isVRC6b  bool
	prg      []byte
	chr      []byte
	chrIsRAM bool
	prgRAM   [0x2000]byte
	battery  bool

	prgBank16 byte // command $8000: 16 KiB bank at $8000-$BFFF
	prgBank8  byte // command $C000: 8 KiB bank at $C000-$DFFF
	chrBanks  [8]byte
	mirroring nes.Mirroring

	// IRQ state — same shape as VRC4.
	irqLatch          byte
	irqCounter        byte
	irqEnable         bool
	irqEnableAfterAck bool
	irqMode           byte
	irqPrescaler      int
	irqPending        bool
	irqSink           IRQSink

	audioSink VRC6AudioSink
}

const vrc6IRQSource = "vrc6"

// VRC6AudioSink is what the cart forwards $9000-$BFFF audio
// register writes to. apu.VRC6Audio satisfies it.
type VRC6AudioSink interface {
	Write(addr uint16, v byte)
}

// NewVRC6 builds a VRC6 cart. Mapper 24 = VRC6a; mapper 26 = VRC6b
// (swapped sub-bits).
func NewVRC6(rom *nes.ROM) (*VRC6, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(8*1024) != 0 {
		return nil, fmt.Errorf("vrc6: PRG must be a non-zero multiple of 8 KiB; got %d bytes", len(rom.PRG))
	}
	c := &VRC6{
		prg:       rom.PRG,
		battery:   rom.Battery,
		mirroring: rom.Mirroring,
		isVRC6b:   rom.Mapper == 26,
	}
	switch {
	case len(rom.CHR) == 0:
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	case len(rom.CHR)%(1*1024) == 0:
		c.chr = rom.CHR
	default:
		return nil, fmt.Errorf("vrc6: CHR must be 0 or a multiple of 1 KiB; got %d bytes", len(rom.CHR))
	}
	return c, nil
}

// SetIRQSink wires the CPU's IRQ surface (optional).
func (c *VRC6) SetIRQSink(s IRQSink) { c.irqSink = s }

// SetAudioSink wires the VRC6 audio chip (optional).
func (c *VRC6) SetAudioSink(s VRC6AudioSink) { c.audioSink = s }

// subBits returns the address-line bit pair selecting the sub-
// register within a class. VRC6a uses A0,A1; VRC6b swaps them.
func (c *VRC6) subBits(addr uint16) byte {
	if c.isVRC6b {
		return byte((addr>>1)&1) | byte(addr&1)<<1
	}
	return byte(addr&1) | byte((addr>>1)&1)<<1
}

// CPURead routes PRG-RAM + bank-switched ROM.
func (c *VRC6) CPURead(addr uint16) byte {
	switch {
	case addr < 0x6000:
		return 0
	case addr < 0x8000:
		return c.prgRAM[addr-0x6000]
	}
	totalBanks8 := len(c.prg) / (8 * 1024)
	lastBank8 := totalBanks8 - 1
	switch {
	case addr < 0xC000:
		// 16 KiB window: bank index = prgBank16 (4 bits) × 2 in 8 KiB units.
		base := int(c.prgBank16&0x0F) * 2
		off := base*8*1024 + int(addr-0x8000)
		return c.prg[off%len(c.prg)]
	case addr < 0xE000:
		off := int(c.prgBank8&0x1F)*8*1024 + int(addr-0xC000)
		return c.prg[off%len(c.prg)]
	default:
		return c.prg[lastBank8*8*1024+int(addr-0xE000)]
	}
}

// CPUWrite handles PRG-RAM + register decode.
func (c *VRC6) CPUWrite(addr uint16, v byte) {
	switch {
	case addr < 0x6000:
		return
	case addr < 0x8000:
		c.prgRAM[addr-0x6000] = v
		return
	}
	class := addr & 0xF000
	sub := c.subBits(addr)
	switch class {
	case 0x8000:
		c.prgBank16 = v
	case 0x9000, 0xA000, 0xB000:
		// Audio writes get forwarded verbatim with the cart-side
		// "logical" address ($9000 sub n / $A000 / $B000) re-built
		// from the class + sub so the audio chip doesn't need to
		// know about the per-variant sub-bit routing.
		logical := class | uint16(sub)
		if class == 0xB000 && sub == 3 {
			// $B003 is the banking + mirroring control byte (NOT
			// an audio register).
			c.applyBankingControl(v)
			return
		}
		if c.audioSink != nil {
			c.audioSink.Write(logical, v)
		}
	case 0xC000:
		c.prgBank8 = v
	case 0xD000:
		c.chrBanks[sub] = v
	case 0xE000:
		c.chrBanks[4+sub] = v
	case 0xF000:
		switch sub {
		case 0:
			c.irqLatch = v
		case 1:
			c.irqEnableAfterAck = v&0x01 != 0
			c.irqEnable = v&0x02 != 0
			c.irqMode = (v >> 2) & 0x01
			if c.irqEnable {
				c.irqCounter = c.irqLatch
				c.irqPrescaler = 341
			}
			c.ackIRQ()
		case 2:
			c.irqEnable = c.irqEnableAfterAck
			c.ackIRQ()
		}
	}
}

// applyBankingControl handles the $B003 register: PPU banking
// mode + nametable mirroring.
func (c *VRC6) applyBankingControl(v byte) {
	switch (v >> 2) & 0x03 {
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

func (c *VRC6) ackIRQ() {
	if c.irqPending {
		c.irqPending = false
		if c.irqSink != nil {
			c.irqSink.ClearIRQSource(vrc6IRQSource)
		}
	}
}

// PPURead serves CHR via the eight 1 KiB bank registers.
func (c *VRC6) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	bank := c.chrBanks[addr>>10]
	off := int(bank)*1024 + int(addr&0x03FF)
	return c.chr[off%len(c.chr)]
}

// PPUWrite is effective only for CHR-RAM carts.
func (c *VRC6) PPUWrite(addr uint16, v byte) {
	if !c.chrIsRAM || addr >= 0x2000 {
		return
	}
	bank := c.chrBanks[addr>>10]
	off := int(bank)*1024 + int(addr&0x03FF)
	c.chr[off%len(c.chr)] = v
}

// Mirroring + housekeeping satisfying the Cartridge interface.
func (c *VRC6) Mirroring() nes.Mirroring { return c.mirroring }
func (c *VRC6) BatteryBacked() bool      { return c.battery }
func (c *VRC6) PRGRAM() []byte           { return c.prgRAM[:] }

// Tick implements cpu.Ticker for the IRQ counter (identical shape
// to VRC4's).
func (c *VRC6) Tick(cycles int) {
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

func (c *VRC6) tickCounter() {
	if c.irqCounter == 0xFF {
		c.irqCounter = c.irqLatch
		if !c.irqPending {
			c.irqPending = true
			if c.irqSink != nil {
				c.irqSink.AssertIRQSource(vrc6IRQSource)
			}
		}
	} else {
		c.irqCounter++
	}
}
