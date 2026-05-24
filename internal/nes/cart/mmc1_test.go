package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// fillMMC1Rom builds an MMC1 ROM with `prgBanks` 16 KiB PRG banks
// and `chrBanks` 4 KiB CHR banks. Each PRG bank's first byte is the
// bank number so per-bank reads are trivially identifiable; same
// for CHR.
func fillMMC1Rom(t *testing.T, prgBanks, chrBanks int) *nes.ROM {
	t.Helper()
	prg := make([]byte, prgBanks*16*1024)
	for i := range prgBanks {
		prg[i*16*1024] = byte(i)
	}
	chr := make([]byte, chrBanks*4*1024)
	for i := range chrBanks {
		chr[i*4*1024] = byte(i)
	}
	return &nes.ROM{Mapper: 1, PRG: prg, CHR: chr}
}

// commitMMC1 simulates the 5-write serial protocol: shift the
// 5-bit value into the chosen destination address.
func commitMMC1(c *MMC1, addr uint16, val byte) {
	for i := range 5 {
		c.CPUWrite(addr, (val>>i)&1)
	}
}

// Power-on default: PRG mode 3 (last bank fixed at $C000, switch at
// $8000). A bare ROM with two PRG banks should show bank 0 at
// $8000 and bank 1 at $C000.
func TestMMC1_PowerOnPRGMode3(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 2, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	if got := c.CPURead(0x8000); got != 0 {
		t.Errorf("$8000 = $%02X; want bank 0", got)
	}
	if got := c.CPURead(0xC000); got != 1 {
		t.Errorf("$C000 = $%02X; want bank 1 (last)", got)
	}
}

// 5-write serial protocol commits to the destination register. Set
// PRG bank to 1 via $E000-$FFFF; $8000 should now read bank 1.
func TestMMC1_SerialShiftCommitsPRGBank(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 4, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	commitMMC1(c, 0xE000, 0x01)
	if got := c.CPURead(0x8000); got != 1 {
		t.Errorf("$8000 = $%02X; want bank 1 after prgBank=1", got)
	}
}

// Bit 7 write resets the shift register + forces PRG mode 3.
// Verify by partially shifting then bit-7-writing, then confirming
// the next 5-write sequence still commits correctly.
func TestMMC1_BitSevenWriteResetsShift(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 4, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	// Two partial writes, then reset.
	c.CPUWrite(0xE000, 1)
	c.CPUWrite(0xE000, 1)
	c.CPUWrite(0xE000, 0x80) // reset
	if c.shift != 0 || c.writeCnt != 0 {
		t.Fatalf("bit-7 didn't reset shift state: shift=$%02X cnt=%d", c.shift, c.writeCnt)
	}
	// Control bits 2-3 should be forced to 11 (PRG mode 3).
	if c.control&0x0C != 0x0C {
		t.Errorf("bit-7 reset didn't OR PRG mode 3 into control; got control=$%02X", c.control)
	}
	// Fresh 5-write sequence still works.
	commitMMC1(c, 0xE000, 0x02)
	if got := c.CPURead(0x8000); got != 2 {
		t.Errorf("post-reset prgBank=2: $8000 = $%02X; want 2", got)
	}
}

// PRG mode 2: first bank fixed at $8000, switch at $C000.
func TestMMC1_PRGMode2_FixFirstSwitchLast(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 4, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	// Control = $08 (PRG mode 2 = 10 in bits 2-3).
	commitMMC1(c, 0x8000, 0x08)
	// prgBank = 2.
	commitMMC1(c, 0xE000, 0x02)
	if got := c.CPURead(0x8000); got != 0 {
		t.Errorf("$8000 in PRG mode 2 = $%02X; want bank 0 (fixed first)", got)
	}
	if got := c.CPURead(0xC000); got != 2 {
		t.Errorf("$C000 in PRG mode 2 = $%02X; want bank 2", got)
	}
}

// PRG mode 0/1: 32 KiB switch at $8000, low bit of prgBank ignored.
func TestMMC1_PRGMode0_32KSwitch(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 4, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	// PRG mode 0.
	commitMMC1(c, 0x8000, 0x00)
	// prgBank = 2 → 32 KiB window starts at bank 2.
	commitMMC1(c, 0xE000, 0x02)
	if got := c.CPURead(0x8000); got != 2 {
		t.Errorf("$8000 = $%02X; want bank 2", got)
	}
	if got := c.CPURead(0xC000); got != 3 {
		t.Errorf("$C000 = $%02X; want bank 3 (high half of 32 KiB)", got)
	}
}

// CHR 4 KiB mode: chrBank0 + chrBank1 independently select $0000 +
// $1000 halves.
func TestMMC1_CHR4KMode(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 2, 4))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	// Control bit 4 = 1 (4 KiB CHR mode). Also PRG mode 3 default
	// (bits 2-3 = 11 → 0x0C); set the full value $10 | $0C = $1C.
	commitMMC1(c, 0x8000, 0x1C)
	// chrBank0 = 2.
	commitMMC1(c, 0xA000, 0x02)
	// chrBank1 = 3.
	commitMMC1(c, 0xC000, 0x03)
	if got := c.PPURead(0x0000); got != 2 {
		t.Errorf("$0000 = $%02X; want CHR bank 2", got)
	}
	if got := c.PPURead(0x1000); got != 3 {
		t.Errorf("$1000 = $%02X; want CHR bank 3", got)
	}
}

// Mirroring control: bits 0-1 of control register flip the
// mirroring scheme at runtime.
func TestMMC1_MirroringRuntimeChange(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 2, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	commitMMC1(c, 0x8000, 0x00) // single-lower
	if c.Mirroring() != nes.MirrorSingleLower {
		t.Errorf("mode 0 = %s; want single-lower", c.Mirroring())
	}
	commitMMC1(c, 0x8000, 0x01) // single-upper
	if c.Mirroring() != nes.MirrorSingleUpper {
		t.Errorf("mode 1 = %s; want single-upper", c.Mirroring())
	}
	commitMMC1(c, 0x8000, 0x02) // vertical
	if c.Mirroring() != nes.MirrorVertical {
		t.Errorf("mode 2 = %s; want vertical", c.Mirroring())
	}
	commitMMC1(c, 0x8000, 0x03) // horizontal
	if c.Mirroring() != nes.MirrorHorizontal {
		t.Errorf("mode 3 = %s; want horizontal", c.Mirroring())
	}
}

// PRG-RAM at $6000-$7FFF round-trips.
func TestMMC1_PRGRAMRoundTrip(t *testing.T) {
	c, err := NewMMC1(fillMMC1Rom(t, 2, 1))
	if err != nil {
		t.Fatalf("NewMMC1: %v", err)
	}
	c.CPUWrite(0x6000, 0xAB)
	c.CPUWrite(0x7FFF, 0xCD)
	if got := c.CPURead(0x6000); got != 0xAB {
		t.Errorf("$6000 = $%02X; want $AB", got)
	}
	if got := c.CPURead(0x7FFF); got != 0xCD {
		t.Errorf("$7FFF = $%02X; want $CD", got)
	}
}

// cart.Open dispatches mapper=1 to MMC1.
func TestCartOpen_DispatchesMMC1(t *testing.T) {
	rom := fillMMC1Rom(t, 2, 1)
	c, err := Open(rom)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := c.(*MMC1); !ok {
		t.Errorf("Open returned %T; want *MMC1", c)
	}
}
