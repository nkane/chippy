package ppu

import "github.com/nkane/chippy/internal/nes"

// PPU bus layout (14 address bits = 16 KiB):
//
//	$0000-$1FFF — pattern tables (cartridge CHR-ROM or CHR-RAM)
//	$2000-$2FFF — four 1 KiB nametable banks
//	$3000-$3EFF — mirrors of $2000-$2EFF
//	$3F00-$3FFF — palette RAM (32 bytes, mirrored every 32 with sub-mirror)
//
// Address bus is masked to 14 bits ($0000-$3FFF) — anything wider wraps.

// busRead routes a PPU-bus read to the right backing store.
func (p *PPU) busRead(addr uint16) byte {
	addr &= 0x3FFF
	switch {
	case addr < 0x2000:
		if p.cart != nil {
			return p.cart.PPURead(addr)
		}
		return 0
	case addr < 0x3F00:
		return p.vram[p.nametableIndex(addr)]
	default:
		return p.palette[paletteIndex(addr)]
	}
}

// busWrite routes a PPU-bus write to the right backing store.
func (p *PPU) busWrite(addr uint16, v byte) {
	addr &= 0x3FFF
	switch {
	case addr < 0x2000:
		if p.cart != nil {
			p.cart.PPUWrite(addr, v)
		}
	case addr < 0x3F00:
		p.vram[p.nametableIndex(addr)] = v
	default:
		// Palette entries are 6-bit on real silicon; mask to avoid
		// surprises in the renderer (e.g. some test ROMs poke $FF).
		p.palette[paletteIndex(addr)] = v & 0x3F
	}
}

// nametableIndex maps a PPU-bus nametable address into the PPU's 2 KiB
// of internal VRAM, honoring the cartridge's mirroring scheme.
//
//	addr & 0x0FFF folds $3000-$3EFF onto $2000-$2EFF.
//	The top two bits of the 12-bit offset select one of four logical
//	nametable banks ($2000 / $2400 / $2800 / $2C00); mirroring then
//	collapses those four banks into the two physical 1 KiB banks.
func (p *PPU) nametableIndex(addr uint16) uint16 {
	addr &= 0x0FFF
	bank := (addr >> 10) & 0x03
	offset := addr & 0x03FF
	var mir nes.Mirroring
	if p.cart != nil {
		mir = p.cart.Mirroring()
	}
	var phys uint16
	switch mir {
	case nes.MirrorHorizontal:
		// A A B B → bank 0,1 → 0; bank 2,3 → 1.
		phys = bank >> 1
	case nes.MirrorVertical:
		// A B A B → bank 0,2 → 0; bank 1,3 → 1.
		phys = bank & 0x01
	case nes.MirrorFourScreen:
		// Real four-screen carts ship extra VRAM. v0.1 has no path to
		// extra VRAM, so we fold to 2 KiB the same way horizontal
		// does — incorrect for those titles, but no NROM cart uses
		// four-screen, so v0.1 doesn't hit it.
		phys = bank >> 1
	case nes.MirrorSingleLower:
		// All four logical nametables map to physical bank 0.
		phys = 0
	case nes.MirrorSingleUpper:
		// All four logical nametables map to physical bank 1.
		phys = 1
	default:
		phys = bank >> 1
	}
	return phys*0x0400 + offset
}

// paletteIndex maps a $3F00-$3FFF read to the 32-byte palette RAM,
// applying the four well-known sub-mirrors:
//
//	$3F10 ↔ $3F00, $3F14 ↔ $3F04, $3F18 ↔ $3F08, $3F1C ↔ $3F0C
//
// On real silicon those four addresses are the "universal background"
// mirror — sprite palette entries at $3F10/$3F14/$3F18/$3F1C are
// hardwired to the background entries.
func paletteIndex(addr uint16) uint16 {
	idx := addr & 0x001F
	if idx >= 0x10 && idx&0x03 == 0 {
		idx -= 0x10
	}
	return idx
}
