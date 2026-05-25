package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// AOROM is mapper 7 — Rare/Nintendo's "AxROM" family. A single
// 32 KiB switchable PRG window at $8000-$FFFF (no fixed bank) plus
// single-screen mirroring whose active nametable the game flips at
// runtime. Battletoads, Marble Madness, R.C. Pro-Am, Wizards &
// Warriors, Jeopardy!.
//
// Register: any write to $8000-$FFFF —
//
//	bits 0-2: 32 KiB PRG bank select (up to 8 banks = 256 KiB)
//	bit 4:    nametable select (0 = single-screen lower, 1 = upper)
//
// CHR is always 8 KiB CHR-RAM (AxROM carts ship no CHR-ROM).
//
// Bus conflicts: AOROM proper has none; the simple-correct write
// path here matches. (The AMROM/ANROM bus-conflict variant is a
// sub-mapper concern, deferred — same pattern as UNROM #319.)
type AOROM struct {
	prg       []byte
	chr       []byte // 8 KiB CHR-RAM
	prgBank   byte
	mirroring nes.Mirroring
}

// NewAOROM builds an AxROM cart. PRG must be a non-zero multiple of
// 32 KiB.
func NewAOROM(rom *nes.ROM) (*AOROM, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(32*1024) != 0 {
		return nil, fmt.Errorf("aorom: PRG must be a non-zero multiple of 32 KiB; got %d bytes", len(rom.PRG))
	}
	c := &AOROM{
		prg: rom.PRG,
		chr: make([]byte, 8*1024),
		// AxROM powers up single-screen lower; the game picks the
		// nametable on its first $8000 write.
		mirroring: nes.MirrorSingleLower,
	}
	if len(rom.CHR) == 8*1024 {
		copy(c.chr, rom.CHR)
	}
	return c, nil
}

// CPURead serves the active 32 KiB bank across $8000-$FFFF.
func (c *AOROM) CPURead(addr uint16) byte {
	if addr < 0x8000 {
		return 0
	}
	totalBanks := len(c.prg) / (32 * 1024)
	bank := int(c.prgBank) % totalBanks
	return c.prg[bank*32*1024+int(addr-0x8000)]
}

// CPUWrite latches the bank select + nametable bit.
func (c *AOROM) CPUWrite(addr uint16, v byte) {
	if addr < 0x8000 {
		return
	}
	c.prgBank = v & 0x07
	if v&0x10 != 0 {
		c.mirroring = nes.MirrorSingleUpper
	} else {
		c.mirroring = nes.MirrorSingleLower
	}
}

// PPURead / PPUWrite serve the 8 KiB CHR-RAM.
func (c *AOROM) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	return c.chr[addr]
}

func (c *AOROM) PPUWrite(addr uint16, v byte) {
	if addr >= 0x2000 {
		return
	}
	c.chr[addr] = v
}

// Mirroring is single-screen, runtime-selected via $8000 bit 4.
func (c *AOROM) Mirroring() nes.Mirroring { return c.mirroring }

// AxROM ships no PRG-RAM slot.
func (c *AOROM) BatteryBacked() bool { return false }
func (c *AOROM) PRGRAM() []byte      { return nil }
