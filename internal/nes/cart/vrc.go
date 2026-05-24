package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// VRC implements Konami's VRC2 + VRC4 mapper family (mappers 21, 22,
// 23, 25). Both chips share the same register-class layout addressed
// at $8000 / $9000 / $A000 / $B000-$E000 / $F000; VRC4 adds a CPU-
// clock-counted IRQ at $F000-$FFFF that VRC2 leaves silent.
//
// What differs between the variants is which CPU address bits encode
// the "sub-bits" (0-3) within each register class — Konami shipped
// the same silicon under multiple pinouts. Each mapper number + sub-
// mapper picks one pinout via subBits().
//
// PRG layout:
//
//	$8000-$9FFF — switchable 8 KiB
//	$A000-$BFFF — switchable 8 KiB
//	$C000-$DFFF — second-to-last 8 KiB (PRG mode 0, the VRC4 default)
//	             OR switchable when PRG mode 1 swaps $8000 with $C000
//	$E000-$FFFF — fixed last 8 KiB
//
// CHR layout: eight independent 1 KiB banks. VRC2a (mapper 22) is
// the outlier — its bank values are pre-shifted right by 1 because
// the chip only routes the upper 7 of the 8-bit bank index pins to
// CHR-ROM.
//
// VRC4 IRQ:
//
//	$F000 / sub 0 — counter reload latch low nibble
//	$F000 / sub 1 — counter reload latch high nibble
//	$F000 / sub 2 — control byte (bit 0 = enableAfterAck, bit 1 = enable,
//	                              bit 2 = mode, 0 = scanline / 1 = CPU)
//	$F000 / sub 3 — acknowledge pending IRQ
//
// Scanline mode runs an internal 341-CPU-cycle prescaler; CPU mode
// ticks the counter every CPU cycle. On $FF -> $00 the counter
// reloads from the latch and the named "vrc4" IRQ source asserts.
type VRC struct {
	variant  vrcVariant
	isVRC4   bool
	prg      []byte
	chr      []byte
	chrIsRAM bool
	prgRAM   [0x2000]byte
	battery  bool

	// Banking state.
	prgBank0  byte // $8000 / $C000 (swapped by prgMode)
	prgBank1  byte // $A000
	prgMode   bool // VRC4 only
	chrBanks  [8]byte
	mirroring nes.Mirroring

	// IRQ state (VRC4 only).
	irqLatch          byte
	irqCounter        byte
	irqEnable         bool
	irqEnableAfterAck bool
	irqMode           byte // 0 = scanline, 1 = CPU
	irqPrescaler      int
	irqPending        bool
	irqSink           IRQSink
}

const vrc4IRQSource = "vrc4"

type vrcVariant int

const (
	vrcVariantVRC2a vrcVariant = iota // mapper 22
	vrcVariantVRC2b                   // mapper 23 default
	vrcVariantVRC2c                   // mapper 25 with sub-mapper VRC2
	vrcVariantVRC4a                   // mapper 21 default
	vrcVariantVRC4b                   // mapper 25 default
	vrcVariantVRC4f                   // mapper 23 with VRC4 sub-mapper
)

// NewVRC builds a VRC2/VRC4 cart. mapper + sub-mapper select the
// variant; default sub-mapper is the most common pinout per mapper
// number.
func NewVRC(rom *nes.ROM) (*VRC, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(8*1024) != 0 {
		return nil, fmt.Errorf("vrc: PRG must be a non-zero multiple of 8 KiB; got %d bytes", len(rom.PRG))
	}
	c := &VRC{
		prg:       rom.PRG,
		battery:   rom.Battery,
		mirroring: rom.Mirroring,
	}
	switch {
	case len(rom.CHR) == 0:
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	case len(rom.CHR)%(1*1024) == 0:
		c.chr = rom.CHR
	default:
		return nil, fmt.Errorf("vrc: CHR must be 0 or a multiple of 1 KiB; got %d bytes", len(rom.CHR))
	}
	switch rom.Mapper {
	case 21:
		c.variant = vrcVariantVRC4a
		c.isVRC4 = true
	case 22:
		c.variant = vrcVariantVRC2a
		c.isVRC4 = false
	case 23:
		// Sub-mapper 3 is VRC2b; the rest (or default 0) are VRC4f.
		if rom.SubMapper == 3 {
			c.variant = vrcVariantVRC2b
			c.isVRC4 = false
		} else {
			c.variant = vrcVariantVRC4f
			c.isVRC4 = true
		}
	case 25:
		// Sub-mapper 3 is VRC2c; default is VRC4b.
		if rom.SubMapper == 3 {
			c.variant = vrcVariantVRC2c
			c.isVRC4 = false
		} else {
			c.variant = vrcVariantVRC4b
			c.isVRC4 = true
		}
	default:
		return nil, fmt.Errorf("vrc: mapper %d not part of VRC2/VRC4 family", rom.Mapper)
	}
	return c, nil
}

// SetIRQSink wires the CPU's IRQ surface (VRC4 only — VRC2 silently
// keeps it).
func (c *VRC) SetIRQSink(s IRQSink) { c.irqSink = s }

// subBits maps a CPU register-write address to its 0..3 sub-bit
// index per the active variant's pinout. The four register-class
// addresses ($8000, $A000, $B000-$E000, $F000) use the sub-bits to
// pick one of four sub-registers within the class.
func (c *VRC) subBits(addr uint16) byte {
	switch c.variant {
	case vrcVariantVRC2a:
		// VRC2a (mapper 22): subbits = A1 << 1 | A0.
		return byte((addr>>1)&1)<<0 | byte((addr>>0)&1)<<1
	case vrcVariantVRC2b, vrcVariantVRC4f:
		// VRC2b / VRC4f (mapper 23 default): A0, A1.
		return byte(addr&1)<<0 | byte((addr>>1)&1)<<1
	case vrcVariantVRC2c, vrcVariantVRC4b:
		// VRC2c / VRC4b (mapper 25): A1, A0 — swapped from above.
		return byte((addr>>1)&1)<<0 | byte(addr&1)<<1
	case vrcVariantVRC4a:
		// VRC4a (mapper 21 default): A1, A6.
		return byte((addr>>1)&1)<<0 | byte((addr>>6)&1)<<1
	}
	return 0
}

// CPURead routes $6000-$FFFF through PRG-RAM + bank-switched ROM.
func (c *VRC) CPURead(addr uint16) byte {
	switch {
	case addr < 0x6000:
		return 0
	case addr < 0x8000:
		return c.prgRAM[addr-0x6000]
	}
	totalBanks := len(c.prg) / (8 * 1024)
	lastBank := totalBanks - 1
	var bank int
	switch {
	case addr < 0xA000:
		if c.prgMode {
			bank = lastBank - 1
		} else {
			bank = int(c.prgBank0) % totalBanks
		}
	case addr < 0xC000:
		bank = int(c.prgBank1) % totalBanks
	case addr < 0xE000:
		if c.prgMode {
			bank = int(c.prgBank0) % totalBanks
		} else {
			bank = lastBank - 1
		}
	default:
		bank = lastBank
	}
	off := bank*8*1024 + int(addr&0x1FFF)
	return c.prg[off]
}

// CPUWrite handles PRG-RAM + register decode.
func (c *VRC) CPUWrite(addr uint16, v byte) {
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
		c.prgBank0 = v & 0x1F
	case 0x9000:
		switch sub {
		case 0, 1:
			c.applyMirroring(v & 0x03)
		case 2, 3:
			if c.isVRC4 {
				c.prgMode = v&0x02 != 0
			}
		}
	case 0xA000:
		c.prgBank1 = v & 0x1F
	case 0xB000, 0xC000, 0xD000, 0xE000:
		// Each register class covers two 1 KiB CHR banks; each bank
		// has a low + high nibble written separately. Class index =
		// ((class - $B000) / $1000) * 2; sub picks which bank +
		// which nibble.
		base := int((class-0xB000)>>12) * 2
		bank := base + int(sub>>1)
		highNibble := sub&1 != 0
		val := v & 0x0F
		if highNibble {
			c.chrBanks[bank] = (c.chrBanks[bank] & 0x0F) | (val << 4)
		} else {
			c.chrBanks[bank] = (c.chrBanks[bank] & 0xF0) | val
		}
		// VRC2a chr bank index is 7 bits — the 8-bit value gets
		// shifted right by 1 before lookup. We store the raw byte
		// and shift at read time, so no normalisation here.
	case 0xF000:
		if !c.isVRC4 {
			return
		}
		switch sub {
		case 0:
			c.irqLatch = (c.irqLatch & 0xF0) | (v & 0x0F)
		case 1:
			c.irqLatch = (c.irqLatch & 0x0F) | ((v & 0x0F) << 4)
		case 2:
			c.irqEnableAfterAck = v&0x01 != 0
			c.irqEnable = v&0x02 != 0
			c.irqMode = (v >> 2) & 0x01
			if c.irqEnable {
				c.irqCounter = c.irqLatch
				c.irqPrescaler = 341
			}
			c.ackIRQ()
		case 3:
			c.irqEnable = c.irqEnableAfterAck
			c.ackIRQ()
		}
	}
}

func (c *VRC) applyMirroring(v byte) {
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

func (c *VRC) ackIRQ() {
	if c.irqPending {
		c.irqPending = false
		if c.irqSink != nil {
			c.irqSink.ClearIRQSource(vrc4IRQSource)
		}
	}
}

// PPURead serves CHR via the eight 1 KiB bank registers.
func (c *VRC) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	return c.chr[c.chrOffset(addr)]
}

// PPUWrite is effective only for CHR-RAM carts.
func (c *VRC) PPUWrite(addr uint16, v byte) {
	if !c.chrIsRAM || addr >= 0x2000 {
		return
	}
	c.chr[c.chrOffset(addr)] = v
}

func (c *VRC) chrOffset(addr uint16) int {
	bank := uint16(c.chrBanks[addr>>10])
	if c.variant == vrcVariantVRC2a {
		// VRC2a: only the upper 7 bits are routed — shift right.
		bank >>= 1
	}
	off := int(bank)*1024 + int(addr&0x03FF)
	return off % len(c.chr)
}

// Mirroring returns the active scheme.
func (c *VRC) Mirroring() nes.Mirroring { return c.mirroring }

// BatteryBacked surfaces the iNES bit for nessy's .sav routing.
func (c *VRC) BatteryBacked() bool { return c.battery }

// PRGRAM exposes the 8 KiB PRG-RAM region for save / restore.
func (c *VRC) PRGRAM() []byte { return c.prgRAM[:] }

// Tick implements cpu.Ticker. VRC4 only; VRC2 leaves the counter
// silent.
func (c *VRC) Tick(cycles int) {
	if !c.isVRC4 || !c.irqEnable {
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

func (c *VRC) tickCounter() {
	if c.irqCounter == 0xFF {
		c.irqCounter = c.irqLatch
		c.fireIRQ()
	} else {
		c.irqCounter++
	}
}

func (c *VRC) fireIRQ() {
	if c.irqPending {
		return
	}
	c.irqPending = true
	if c.irqSink != nil {
		c.irqSink.AssertIRQSource(vrc4IRQSource)
	}
}
