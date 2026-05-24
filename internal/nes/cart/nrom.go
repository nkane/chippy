package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// NROM is mapper 0: the simplest NES cartridge configuration.
//
//	PRG-ROM: 16 KiB or 32 KiB, fixed mapping.
//	  32 KiB cart → $8000-$FFFF maps directly to bank.
//	  16 KiB cart → $8000-$BFFF maps to bank; $C000-$FFFF mirrors it.
//	CHR-ROM: 8 KiB at $0000-$1FFF on the PPU bus.
//	No bank switching. No PRG-RAM (iNES battery flag is ignored).
//	PRG writes are silent no-ops (the address space is read-only on
//	silicon — but writes don't fault, they just don't land).
//
// CHR-RAM variant: when the iNES header advertises 0 CHR banks the
// cartridge has 8 KiB of CHR-RAM at the PPU bus instead. PPUWrite
// effective; PPURead returns the last value written.
type NROM struct {
	prg       []byte
	chr       []byte // 8 KiB; CHR-RAM if rom.CHR was nil
	chrIsRAM  bool
	mirroring nes.Mirroring
}

// NewNROM constructs an NROM cart from a parsed iNES ROM. Rejects
// PRG sizes outside the 16 KiB / 32 KiB envelope.
func NewNROM(rom *nes.ROM) (*NROM, error) {
	switch len(rom.PRG) {
	case 16 * 1024, 32 * 1024:
	default:
		return nil, fmt.Errorf("nrom: PRG must be 16 or 32 KiB; got %d bytes", len(rom.PRG))
	}
	c := &NROM{
		prg:       rom.PRG,
		mirroring: rom.Mirroring,
	}
	if len(rom.CHR) == 0 {
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	} else if len(rom.CHR) == 8*1024 {
		c.chr = rom.CHR
	} else {
		return nil, fmt.Errorf("nrom: CHR must be 0 or 8 KiB; got %d bytes", len(rom.CHR))
	}
	return c, nil
}

// CPURead: addresses below $8000 are unmapped on NROM and return open-bus.
// Real silicon leaves the data bus floating; we return 0 deterministically.
func (c *NROM) CPURead(addr uint16) byte {
	if addr < 0x8000 {
		return 0
	}
	// For 16 KiB carts, $C000-$FFFF mirrors $8000-$BFFF.
	idx := int(addr-0x8000) % len(c.prg)
	return c.prg[idx]
}

// CPUWrite is a no-op for NROM. Real silicon has no path to write
// the ROM; we drop quietly rather than logging — debug breakpoints
// catch errant writes more usefully.
func (c *NROM) CPUWrite(addr uint16, v byte) {}

func (c *NROM) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		// PPU bus addresses above $1FFF belong to nametables /
		// palettes, owned by the PPU itself, not the cart.
		return 0
	}
	return c.chr[addr]
}

func (c *NROM) PPUWrite(addr uint16, v byte) {
	if addr >= 0x2000 || !c.chrIsRAM {
		return
	}
	c.chr[addr] = v
}

func (c *NROM) Mirroring() nes.Mirroring { return c.mirroring }

// NROM has no PRG-RAM slot — never battery-backed.
func (c *NROM) BatteryBacked() bool { return false }
func (c *NROM) PRGRAM() []byte      { return nil }
