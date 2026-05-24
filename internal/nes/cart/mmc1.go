package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// MMC1 is mapper 1 — Nintendo's first MMC ASIC. Unlocks Zelda 1,
// Final Fantasy, Metroid, Castlevania II, Dragon Warrior, and most
// other early big-cart titles.
//
// PRG: up to 256 KiB across 16 KiB banks, mapped at $8000-$BFFF
// and $C000-$FFFF per the PRG bank mode (control bits 2-3).
// CHR: up to 128 KiB across 4 KiB banks, mapped at $0000-$0FFF and
// $1000-$1FFF per the CHR bank mode (control bit 4).
// Optional 8 KiB PRG-RAM at $6000-$7FFF.
//
// Writes to $8000-$FFFF serially shift a 5-bit register one bit at
// a time (bit 0 of each write); on the fifth write the assembled
// value writes through to one of four internal registers chosen by
// the destination address (bits 13-14). A write with bit 7 set
// resets the shift register and ORs control bits 2-3 (PRG mode = 3
// per nesdev). Real silicon also has a "consecutive cycle" bug:
// back-to-back writes from RMW opcodes get ignored — out of scope
// for v0.3.
type MMC1 struct {
	prg      []byte
	chr      []byte
	chrIsRAM bool
	prgRAM   [0x2000]byte
	battery  bool // iNES flag6 bit 1; routes save to a sibling .sav

	// Shift register state.
	shift    byte // 5-bit accumulator
	writeCnt byte // 0..4

	// Latched control registers (commit on the fifth shift write).
	control  byte // $8000-$9FFF — mirroring, PRG mode, CHR mode
	chrBank0 byte // $A000-$BFFF
	chrBank1 byte // $C000-$DFFF
	prgBank  byte // $E000-$FFFF

	// Cached mirroring derived from control bits 0-1; updated on
	// every write to the control register.
	mirroring nes.Mirroring
}

// NewMMC1 constructs an MMC1 cart from a parsed iNES ROM. PRG must
// be a multiple of 16 KiB; CHR is either 0 (CHR-RAM) or a multiple
// of 4 KiB.
func NewMMC1(rom *nes.ROM) (*MMC1, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(16*1024) != 0 {
		return nil, fmt.Errorf("mmc1: PRG must be a non-zero multiple of 16 KiB; got %d bytes", len(rom.PRG))
	}
	c := &MMC1{
		prg: rom.PRG,
		// MMC1 powers up with PRG mode 3 latched (last bank fixed at
		// $C000, switch at $8000) per nesdev. control bits 2-3 = 11.
		control:   0x0C,
		mirroring: rom.Mirroring,
		battery:   rom.Battery,
	}
	switch {
	case len(rom.CHR) == 0:
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	case len(rom.CHR)%(4*1024) == 0:
		c.chr = rom.CHR
	default:
		return nil, fmt.Errorf("mmc1: CHR must be 0 or a multiple of 4 KiB; got %d bytes", len(rom.CHR))
	}
	c.applyControlMirroring()
	return c, nil
}

// CPURead handles the $6000-$FFFF window: $6000-$7FFF is the
// optional 8 KiB PRG-RAM; $8000-$FFFF is two 16 KiB PRG banks
// selected by the active PRG bank mode + prgBank register.
func (c *MMC1) CPURead(addr uint16) byte {
	switch {
	case addr < 0x6000:
		return 0
	case addr < 0x8000:
		return c.prgRAM[addr-0x6000]
	}
	bankSize := 16 * 1024
	totalBanks := len(c.prg) / bankSize
	lastBank := totalBanks - 1
	prgMode := (c.control >> 2) & 0x03
	var bank int
	switch prgMode {
	case 0, 1:
		// Switch 32 KiB at $8000, ignoring low bit of prgBank.
		bank32 := int(c.prgBank & 0x0E)
		off := int(addr-0x8000) + bank32*bankSize
		return c.prg[off%len(c.prg)]
	case 2:
		// Fix first bank at $8000, switch 16 KiB at $C000.
		if addr < 0xC000 {
			bank = 0
		} else {
			bank = int(c.prgBank & 0x0F)
		}
	case 3:
		// Switch 16 KiB at $8000, fix last bank at $C000.
		if addr < 0xC000 {
			bank = int(c.prgBank & 0x0F)
		} else {
			bank = lastBank
		}
	}
	off := int(addr&0x3FFF) + bank*bankSize
	return c.prg[off%len(c.prg)]
}

// CPUWrite at $6000-$7FFF lands in PRG-RAM; at $8000-$FFFF feeds the
// serial shift register. Writes elsewhere are no-ops.
func (c *MMC1) CPUWrite(addr uint16, v byte) {
	switch {
	case addr < 0x6000:
		return
	case addr < 0x8000:
		c.prgRAM[addr-0x6000] = v
		return
	}
	// Reset shift register on bit-7 write + force PRG mode bits to 3.
	if v&0x80 != 0 {
		c.shift = 0
		c.writeCnt = 0
		c.control |= 0x0C
		c.applyControlMirroring()
		return
	}
	c.shift |= (v & 1) << c.writeCnt
	c.writeCnt++
	if c.writeCnt < 5 {
		return
	}
	// Fifth write: commit the 5-bit value to one of four registers
	// based on destination-address bits 13-14.
	dest := (addr >> 13) & 0x03
	switch dest {
	case 0:
		c.control = c.shift & 0x1F
		c.applyControlMirroring()
	case 1:
		c.chrBank0 = c.shift & 0x1F
	case 2:
		c.chrBank1 = c.shift & 0x1F
	case 3:
		c.prgBank = c.shift & 0x1F
	}
	c.shift = 0
	c.writeCnt = 0
}

// applyControlMirroring caches the mirroring scheme derived from
// control bits 0-1 so Mirroring() stays O(1).
func (c *MMC1) applyControlMirroring() {
	switch c.control & 0x03 {
	case 0:
		c.mirroring = nes.MirrorSingleLower
	case 1:
		c.mirroring = nes.MirrorSingleUpper
	case 2:
		c.mirroring = nes.MirrorVertical
	case 3:
		c.mirroring = nes.MirrorHorizontal
	}
}

// PPURead reads from CHR-ROM / CHR-RAM at $0000-$1FFF. The bank
// mode (control bit 4) picks 8 KiB swap (single 8 KiB bank chosen
// by chrBank0 & 0x1E) vs 4 KiB swap (two 4 KiB banks chosen
// independently by chrBank0 and chrBank1).
func (c *MMC1) PPURead(addr uint16) byte {
	if addr >= 0x2000 {
		return 0
	}
	off := c.chrOffset(addr)
	return c.chr[off%len(c.chr)]
}

// PPUWrite is effective only for CHR-RAM carts.
func (c *MMC1) PPUWrite(addr uint16, v byte) {
	if addr >= 0x2000 || !c.chrIsRAM {
		return
	}
	off := c.chrOffset(addr)
	c.chr[off%len(c.chr)] = v
}

// chrOffset computes the byte offset into c.chr for a given PPU
// address, honoring the 8 KiB / 4 KiB CHR bank mode.
func (c *MMC1) chrOffset(addr uint16) int {
	if c.control&0x10 == 0 {
		// 8 KiB mode: chrBank0 low bit ignored.
		bank := int(c.chrBank0 & 0x1E)
		return bank*0x1000 + int(addr)
	}
	// 4 KiB mode: $0000-$0FFF from chrBank0, $1000-$1FFF from chrBank1.
	if addr < 0x1000 {
		return int(c.chrBank0)*0x1000 + int(addr)
	}
	return int(c.chrBank1)*0x1000 + int(addr-0x1000)
}

// Mirroring returns the cached mode (updated on every control
// register write).
func (c *MMC1) Mirroring() nes.Mirroring { return c.mirroring }

// BatteryBacked reports the iNES header's battery bit. ROMs with
// the bit set (Zelda, Final Fantasy, Metroid via password) expect
// their $6000-$7FFF PRG-RAM to survive power-off.
func (c *MMC1) BatteryBacked() bool { return c.battery }

// PRGRAM exposes the 8 KiB PRG-RAM region for save / restore.
// Aliased; callers must copy if detachment is needed.
func (c *MMC1) PRGRAM() []byte { return c.prgRAM[:] }
