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

	// BatteryBacked reports whether the cart's PRG-RAM ($6000-$7FFF)
	// is supposed to persist across power-off (iNES flag6 bit 1).
	// nessy uses this to decide whether to read/write a sibling
	// .sav file. NROM / UxROM / CNROM all return false (no PRG-RAM
	// slot or non-battery). MMC1 / MMC3 honor the header bit.
	BatteryBacked() bool

	// PRGRAM exposes the 8 KiB PRG-RAM region for save / restore.
	// Returns nil when the cart has no PRG-RAM (NROM / UxROM /
	// CNROM). The returned slice aliases internal storage —
	// callers must copy if they need to detach.
	PRGRAM() []byte
}

// Open dispatches on the parsed ROM's Mapper byte and returns the
// concrete cart. v0.3 ships NROM (mapper 0) + MMC1 (mapper 1);
// other mappers return an explicit error so the user can see why
// the ROM didn't boot.
func Open(rom *nes.ROM) (Cartridge, error) {
	switch rom.Mapper {
	case 0:
		return NewNROM(rom)
	case 1:
		return NewMMC1(rom)
	case 2:
		return NewUxROM(rom)
	case 3:
		return NewCNROM(rom)
	case 4:
		return NewMMC3(rom)
	default:
		return nil, fmt.Errorf("cart: unsupported mapper %d (NROM/0, MMC1/1, UxROM/2, CNROM/3, MMC3/4)", rom.Mapper)
	}
}
