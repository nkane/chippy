// Package cart hosts NES cartridge mappers. The CPU side of the
// cartridge claims $4020-$FFFF on the main bus; the PPU side claims
// $0000-$1FFF on the PPU bus (pattern tables) plus sometimes the
// nametable mirroring scheme.
//
// v0.1 ships only mapper 0 (NROM). MMC1 / UxROM / MMC3 land in v0.2+.
package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// Cartridge is the contract a mapper implements. The CPU bus and PPU
// bus are separate in NES topology so the cart exposes both sides;
// chippy's cpu.MMIO will route $4020-$FFFF CPU reads/writes here, and
// the PPU's own bus dispatches $0000-$1FFF directly to PPURead/PPUWrite.
type Cartridge interface {
	// CPU-side bus ($4020-$FFFF). Most carts only respond to $8000+;
	// $4020-$7FFF is for expansion-area carts (Famicom Disk System,
	// some PRG-RAM regions). Mappers ignore the lower range when not
	// in use.
	CPURead(addr uint16) byte
	CPUWrite(addr uint16, v byte)

	// PPU-side bus ($0000-$1FFF). Pattern tables live here. CHR-RAM
	// carts make writes effective; CHR-ROM carts ignore them.
	PPURead(addr uint16) byte
	PPUWrite(addr uint16, v byte)

	// Mirroring is the nametable mirroring scheme the PPU should use.
	// Most carts pin this at construction; MMC1+ later mappers can
	// flip it dynamically (defer to v0.2+).
	Mirroring() nes.Mirroring
}

// Open dispatches on the parsed ROM's Mapper byte and returns the
// concrete cart. v0.1 supports only NROM (mapper 0); other mappers
// return an explicit error so the user can see why the ROM didn't
// boot.
func Open(rom *nes.ROM) (Cartridge, error) {
	switch rom.Mapper {
	case 0:
		return NewNROM(rom)
	default:
		return nil, fmt.Errorf("cart: unsupported mapper %d (v0.1 ships only NROM/mapper 0)", rom.Mapper)
	}
}
