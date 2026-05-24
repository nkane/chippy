package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// FME-7 / Sunsoft 5B (mapper 69). Late-NES Sunsoft mapper used by
// Gimmick! and Batman: Return of the Joker.
//
// PRG layout (8 KiB windows):
//
//	$6000-$7FFF — switchable, sourceable from PRG-ROM or PRG-RAM via
//	              command 8's bits 7/6.
//	$8000-$9FFF — switchable, command 9 selects the bank.
//	$A000-$BFFF — switchable, command 10.
//	$C000-$DFFF — switchable, command 11.
//	$E000-$FFFF — fixed to the last 8 KiB bank.
//
// CHR layout (1 KiB windows): eight switchable 1 KiB banks selected
// by commands 0-7.
//
// Register interface: command/parameter port pair.
//
//	$8000-$9FFF write: 4-bit command (low nibble of the value).
//	$A000-$BFFF write: parameter byte for the latched command.
//
// Mirroring: command 12 bits 0-1 — vertical / horizontal /
// single-screen-lower / single-screen-upper.
//
// IRQ: 16-bit down-counter ticking at CPU clock when command 13 bit
// 7 (counter enable) is set. On underflow, if command 13 bit 0 (IRQ
// enable) is set, the named "fme7" IRQ source asserts on the CPU.
// Counter continues to wrap. Writing command 13 acks any pending IRQ.
//
// Sunsoft 5B audio (three pulse channels from a YM2149 clone) is
// deliberately deferred to v0.6 — out of scope here. Writes to its
// $C000/$E000 audio register pair fall through as no-ops.
type FME7 struct {
	prg      []byte
	chr      []byte
	chrIsRAM bool
	prgRAM   [0x2000]byte
	battery  bool

	command byte // last command latched via $8000

	// Bank registers populated by command writes through $A000.
	chrBanks [8]byte // commands 0-7 (1 KiB each)
	prgRAMBk byte    // command 8: bits 5-0 = bank, bit 6 = RAM/ROM, bit 7 = RAM enable
	prgBanks [3]byte // commands 9/10/11 → $8000 / $A000 / $C000

	mirroring nes.Mirroring

	// IRQ state.
	irqCountEnable bool
	irqEnable      bool
	irqCounter     uint16
	irqPending     bool

	irqSink IRQSink
}

const fme7IRQSource = "fme7"

// NewFME7 constructs an FME-7 cart from a parsed iNES ROM.
func NewFME7(rom *nes.ROM) (*FME7, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(8*1024) != 0 {
		return nil, fmt.Errorf("fme7: PRG must be a non-zero multiple of 8 KiB; got %d bytes", len(rom.PRG))
	}
	c := &FME7{
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
		return nil, fmt.Errorf("fme7: CHR must be 0 or a multiple of 1 KiB; got %d bytes", len(rom.CHR))
	}
	return c, nil
}

// SetIRQSink wires the CPU's IRQ surface. Optional; without a sink
// the IRQ flag still tracks but nothing asserts on the CPU line.
func (c *FME7) SetIRQSink(s IRQSink) { c.irqSink = s }

// CPURead handles $6000-$FFFF.
func (c *FME7) CPURead(addr uint16) byte {
	switch {
	case addr < 0x6000:
		return 0
	case addr < 0x8000:
		// PRG-RAM enable: command 8 bit 7 = enable; bit 6 = RAM mode
		// (else this window reads from PRG-ROM bank cmd8 bits 0-5).
		if c.prgRAMBk&0x80 != 0 && c.prgRAMBk&0x40 != 0 {
			return c.prgRAM[addr-0x6000]
		}
		return c.prgAtBank(c.prgRAMBk&0x3F, addr-0x6000)
	case addr < 0xA000:
		return c.prgAtBank(c.prgBanks[0]&0x3F, addr-0x8000)
	case addr < 0xC000:
		return c.prgAtBank(c.prgBanks[1]&0x3F, addr-0xA000)
	case addr < 0xE000:
		return c.prgAtBank(c.prgBanks[2]&0x3F, addr-0xC000)
	default:
		// Fixed last 8 KiB bank.
		return c.prgAtBank(byte((len(c.prg)/(8*1024))-1), addr-0xE000)
	}
}

func (c *FME7) prgAtBank(bank byte, off uint16) byte {
	totalBanks := len(c.prg) / (8 * 1024)
	idx := int(bank) % totalBanks
	return c.prg[idx*8*1024+int(off)]
}

// CPUWrite handles PRG-RAM (when RAM-enabled at $6000-$7FFF) and the
// command/parameter port pair.
func (c *FME7) CPUWrite(addr uint16, v byte) {
	switch {
	case addr < 0x6000:
		return
	case addr < 0x8000:
		if c.prgRAMBk&0x80 != 0 && c.prgRAMBk&0x40 != 0 {
			c.prgRAM[addr-0x6000] = v
		}
		return
	case addr < 0xA000:
		// Command latch.
		c.command = v & 0x0F
		return
	case addr < 0xC000:
		c.applyParameter(v)
		return
	default:
		// $C000-$FFFF would address the Sunsoft 5B audio register pair
		// on the audio variant of the chip. v0.5 ships FME-7 without
		// the audio expansion; treat as no-op.
		return
	}
}

// applyParameter writes value v into the register selected by the
// most recently latched command.
func (c *FME7) applyParameter(v byte) {
	switch c.command {
	case 0, 1, 2, 3, 4, 5, 6, 7:
		c.chrBanks[c.command] = v
	case 8:
		c.prgRAMBk = v
	case 9, 10, 11:
		c.prgBanks[c.command-9] = v
	case 12:
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
	case 13:
		c.irqCountEnable = v&0x80 != 0
		c.irqEnable = v&0x01 != 0
		// Writing this register acks any pending IRQ.
		if c.irqPending {
			c.irqPending = false
			if c.irqSink != nil {
				c.irqSink.ClearIRQSource(fme7IRQSource)
			}
		}
	case 14:
		c.irqCounter = (c.irqCounter & 0xFF00) | uint16(v)
	case 15:
		c.irqCounter = (c.irqCounter & 0x00FF) | (uint16(v) << 8)
	}
}

// PPURead serves CHR through the eight 1 KiB bank windows.
func (c *FME7) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	bank := c.chrBanks[addr>>10]
	off := uint16(bank)*1024 + (addr & 0x03FF)
	return c.chr[int(off)%len(c.chr)]
}

// PPUWrite is effective only when CHR-RAM is present (rare for FME-7
// but tracked for completeness).
func (c *FME7) PPUWrite(addr uint16, v byte) {
	if !c.chrIsRAM || addr >= 0x2000 {
		return
	}
	bank := c.chrBanks[addr>>10]
	off := uint16(bank)*1024 + (addr & 0x03FF)
	c.chr[int(off)%len(c.chr)] = v
}

// Mirroring returns the currently active scheme.
func (c *FME7) Mirroring() nes.Mirroring { return c.mirroring }

// BatteryBacked surfaces the iNES flag for nessy's .sav routing.
func (c *FME7) BatteryBacked() bool { return c.battery }

// PRGRAM exposes the 8 KiB PRG-RAM region for save / restore.
func (c *FME7) PRGRAM() []byte { return c.prgRAM[:] }

// Tick implements cpu.Ticker. Decrements the 16-bit IRQ counter at
// CPU rate when counter-enable is set; on underflow, if IRQ-enable
// is set, asserts the IRQ source. Counter wraps and keeps counting
// per nesdev.
func (c *FME7) Tick(cycles int) {
	if !c.irqCountEnable {
		return
	}
	for range cycles {
		// Pre-decrement: $0000 wraps to $FFFF and fires an underflow
		// on this tick.
		if c.irqCounter == 0 {
			c.irqCounter = 0xFFFF
			if c.irqEnable && !c.irqPending {
				c.irqPending = true
				if c.irqSink != nil {
					c.irqSink.AssertIRQSource(fme7IRQSource)
				}
			}
		} else {
			c.irqCounter--
		}
	}
}
