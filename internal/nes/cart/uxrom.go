package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// UxROM is mapper 2 — Nintendo's "UNROM" / "UOROM" family. Used by
// Mega Man 1+2, Castlevania, Contra, DuckTales, Metal Gear, and
// most early UxROM titles.
//
//	PRG: up to 256 KiB across 16 KiB banks.
//	  $8000-$BFFF — switchable bank (low 4 bits of CPUWrite data).
//	  $C000-$FFFF — last 16 KiB bank, hardwired.
//	CHR: 8 KiB CHR-RAM (no CHR bank switching; iNES CHR banks
//	     ignored — most UxROM carts ship without CHR-ROM).
//	Mirroring: pinned at construction from the iNES header.
//	Writes to $8000-$FFFF set the PRG bank directly (no shift
//	register — value bits 0-3 select the bank).
//
// Real UNROM silicon has a bus conflict: the written value gets
// ANDed with whatever byte the ROM has at the same address before
// committing to the bank-select register. Most authored ROMs work
// around this by writing through ROM addresses that read \$FF (so
// the AND is a no-op), but a handful test the quirk explicitly.
// Selected via iNES 2.0 sub-mapper 2 (#319).
type UxROM struct {
	prg       []byte
	chr       []byte // 8 KiB CHR-RAM
	mirroring nes.Mirroring
	prgBank   byte // active bank at $8000-$BFFF
	busConfl  bool // iNES 2.0 sub-mapper 2 → ANDs writes with ROM byte
}

// NewUxROM constructs a UxROM cart from a parsed iNES ROM. PRG must
// be a non-zero multiple of 16 KiB.
func NewUxROM(rom *nes.ROM) (*UxROM, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(16*1024) != 0 {
		return nil, fmt.Errorf("uxrom: PRG must be a non-zero multiple of 16 KiB; got %d bytes", len(rom.PRG))
	}
	c := &UxROM{
		prg:       rom.PRG,
		chr:       make([]byte, 8*1024),
		mirroring: rom.Mirroring,
		busConfl:  rom.SubMapper == 2,
	}
	// If the ROM ships CHR-ROM (rare for UxROM), copy it into the
	// CHR-RAM slot so PPU reads still work. Writes from the game
	// will then overwrite — same lossy-but-functional behavior as
	// NROM's CHR-RAM fallback.
	if len(rom.CHR) == 8*1024 {
		copy(c.chr, rom.CHR)
	}
	return c, nil
}

// CPURead serves $8000-$BFFF from the switchable bank, $C000-$FFFF
// from the last bank. $4020-$7FFF is unmapped on UxROM and returns
// open-bus 0.
func (c *UxROM) CPURead(addr uint16) byte {
	if addr < 0x8000 {
		return 0
	}
	bankSize := 16 * 1024
	totalBanks := len(c.prg) / bankSize
	var bank int
	if addr < 0xC000 {
		bank = int(c.prgBank) % totalBanks
	} else {
		bank = totalBanks - 1
	}
	off := int(addr&0x3FFF) + bank*bankSize
	return c.prg[off%len(c.prg)]
}

// CPUWrite sets the PRG bank from data bits 0-3. Real UxROM uses
// only 3-4 bits depending on cart-size variant; we accept the full
// nibble + modulo by the actual bank count in CPURead to handle
// either case.
//
// UNROM bus-conflict variant (sub-mapper 2): ANDs the written byte
// with the ROM byte at the same address before committing. ROMs
// that depend on this almost always write through pages reading
// $FF so the AND is a no-op; the path matters only for ROMs that
// explicitly test the quirk.
func (c *UxROM) CPUWrite(addr uint16, v byte) {
	if addr < 0x8000 {
		return
	}
	if c.busConfl {
		v &= c.CPURead(addr)
	}
	c.prgBank = v & 0x0F
}

// PPURead serves the 8 KiB CHR-RAM at $0000-$1FFF.
func (c *UxROM) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	return c.chr[addr]
}

// PPUWrite to CHR-RAM is effective (writes land in the 8 KiB pool).
func (c *UxROM) PPUWrite(addr uint16, v byte) {
	if addr >= 0x2000 {
		return
	}
	c.chr[addr] = v
}

// Mirroring is pinned at construction from the iNES header.
func (c *UxROM) Mirroring() nes.Mirroring { return c.mirroring }

// UxROM ships no PRG-RAM slot — never battery-backed.
func (c *UxROM) BatteryBacked() bool { return false }
func (c *UxROM) PRGRAM() []byte      { return nil }
