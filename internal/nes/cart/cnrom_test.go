package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func fillCnromRom(t *testing.T, prgKB, chrBanks int) *nes.ROM {
	t.Helper()
	prg := make([]byte, prgKB*1024)
	for i := range prg {
		prg[i] = 0xEA // NOP filler
	}
	chr := make([]byte, chrBanks*8*1024)
	for i := range chrBanks {
		chr[i*8*1024] = byte(i)
	}
	return &nes.ROM{Mapper: 3, PRG: prg, CHR: chr}
}

// Power-on: CHR bank 0 active.
func TestCNROM_PowerOnBank0(t *testing.T) {
	c, err := NewCNROM(fillCnromRom(t, 16, 4))
	if err != nil {
		t.Fatalf("NewCNROM: %v", err)
	}
	if got := c.PPURead(0x0000); got != 0 {
		t.Errorf("PPU $0000 = $%02X; want bank 0", got)
	}
}

// CPUWrite to $8000-$FFFF switches the CHR bank.
func TestCNROM_BankSwitch(t *testing.T) {
	c, err := NewCNROM(fillCnromRom(t, 16, 4))
	if err != nil {
		t.Fatalf("NewCNROM: %v", err)
	}
	c.CPUWrite(0x8000, 0x02)
	if got := c.PPURead(0x0000); got != 2 {
		t.Errorf("post-switch PPU $0000 = $%02X; want bank 2", got)
	}
	c.CPUWrite(0xFFFF, 0x03)
	if got := c.PPURead(0x0000); got != 3 {
		t.Errorf("post-switch PPU $0000 = $%02X; want bank 3", got)
	}
}

// PRG read independent of CHR bank.
func TestCNROM_PRGUnaffectedByCHRSwitch(t *testing.T) {
	c, err := NewCNROM(fillCnromRom(t, 32, 4))
	if err != nil {
		t.Fatalf("NewCNROM: %v", err)
	}
	c.CPUWrite(0x8000, 0x02) // change CHR
	if got := c.CPURead(0x8000); got != 0xEA {
		t.Errorf("PRG read after CHR switch = $%02X; want $EA", got)
	}
}

// 16-KiB PRG mirrors at $C000.
func TestCNROM_PRG16KMirror(t *testing.T) {
	c, err := NewCNROM(fillCnromRom(t, 16, 1))
	if err != nil {
		t.Fatalf("NewCNROM: %v", err)
	}
	if c.CPURead(0x8000) != c.CPURead(0xC000) {
		t.Errorf("16-KiB PRG should mirror $8000 ↔ $C000")
	}
}

// CHR-RAM fallback when ROM ships no CHR.
func TestCNROM_CHRRAMFallback(t *testing.T) {
	rom := fillCnromRom(t, 16, 0)
	rom.CHR = nil
	c, err := NewCNROM(rom)
	if err != nil {
		t.Fatalf("NewCNROM: %v", err)
	}
	c.PPUWrite(0x0100, 0xAA)
	if got := c.PPURead(0x0100); got != 0xAA {
		t.Errorf("CHR-RAM round-trip failed: $%02X", got)
	}
}

// cart.Open dispatches mapper=3 to CNROM.
func TestCartOpen_DispatchesCNROM(t *testing.T) {
	rom := fillCnromRom(t, 16, 1)
	c, err := Open(rom)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := c.(*CNROM); !ok {
		t.Errorf("Open returned %T; want *CNROM", c)
	}
}
