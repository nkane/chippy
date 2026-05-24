package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// CNROM is mapper 3 — Nintendo's simplest CHR-switch family. Used by
// Arkanoid, Bump'n'Jump, Cybernoid, and many early sport titles.
//
//	PRG: fixed 16 KiB or 32 KiB at $8000-$FFFF (same as NROM).
//	CHR: 8 KiB switchable bank at PPU $0000-$1FFF; up to 2 MiB
//	     across 256 banks (rare). Most CNROM ROMs ship 4 banks.
//	Mirroring: pinned at construction from the iNES header.
//	Writes to $8000-$FFFF set the CHR bank (low 2 bits typically;
//	we accept the full byte and modulo by bank count).
//
// Real silicon has a CNROM-512 variant with bus-conflict behavior;
// v0.4 ships the simple correct version.
type CNROM struct {
	prg       []byte
	chr       []byte
	chrIsRAM  bool
	mirroring nes.Mirroring
	chrBank   byte
}

// NewCNROM constructs a CNROM cart. PRG must be 16 or 32 KiB; CHR
// must be a positive multiple of 8 KiB (CHR-RAM-only CNROM is rare
// but allowed — same fallback as NROM).
func NewCNROM(rom *nes.ROM) (*CNROM, error) {
	switch len(rom.PRG) {
	case 16 * 1024, 32 * 1024:
	default:
		return nil, fmt.Errorf("cnrom: PRG must be 16 or 32 KiB; got %d bytes", len(rom.PRG))
	}
	c := &CNROM{
		prg:       rom.PRG,
		mirroring: rom.Mirroring,
	}
	switch {
	case len(rom.CHR) == 0:
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	case len(rom.CHR)%(8*1024) == 0:
		c.chr = rom.CHR
	default:
		return nil, fmt.Errorf("cnrom: CHR must be 0 or a multiple of 8 KiB; got %d bytes", len(rom.CHR))
	}
	return c, nil
}

// CPURead serves $8000-$FFFF from PRG (NROM-style 16-KiB mirror or
// flat 32-KiB). $4020-$7FFF returns open-bus 0.
func (c *CNROM) CPURead(addr uint16) byte {
	if addr < 0x8000 {
		return 0
	}
	idx := int(addr-0x8000) % len(c.prg)
	return c.prg[idx]
}

// CPUWrite to $8000-$FFFF sets the CHR bank. Real silicon honors
// only the low 2 bits on most carts; the modulo in CPURead handles
// the variable bank count.
func (c *CNROM) CPUWrite(addr uint16, v byte) {
	if addr < 0x8000 {
		return
	}
	c.chrBank = v
}

// PPURead serves the active 8 KiB CHR bank at PPU $0000-$1FFF.
func (c *CNROM) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	totalBanks := len(c.chr) / (8 * 1024)
	if totalBanks == 0 {
		totalBanks = 1
	}
	bank := int(c.chrBank) % totalBanks
	return c.chr[bank*8*1024+int(addr)]
}

// PPUWrite is effective only when the cart shipped no CHR-ROM
// (CHR-RAM fallback).
func (c *CNROM) PPUWrite(addr uint16, v byte) {
	if addr >= 0x2000 || !c.chrIsRAM {
		return
	}
	c.chr[addr] = v
}

// Mirroring is pinned at construction.
func (c *CNROM) Mirroring() nes.Mirroring { return c.mirroring }
