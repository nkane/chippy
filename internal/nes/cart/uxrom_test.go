package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// fillUxromRom builds a UxROM ROM with `prgBanks` 16 KiB PRG banks.
// Each bank's first byte = bank number for trivial identification.
func fillUxromRom(t *testing.T, prgBanks int) *nes.ROM {
	t.Helper()
	prg := make([]byte, prgBanks*16*1024)
	for i := range prgBanks {
		prg[i*16*1024] = byte(i)
	}
	return &nes.ROM{Mapper: 2, PRG: prg}
}

// Power-on default: bank 0 at $8000, last bank at $C000.
func TestUxROM_PowerOnDefault(t *testing.T) {
	c, err := NewUxROM(fillUxromRom(t, 4))
	if err != nil {
		t.Fatalf("NewUxROM: %v", err)
	}
	if got := c.CPURead(0x8000); got != 0 {
		t.Errorf("$8000 = $%02X; want bank 0", got)
	}
	if got := c.CPURead(0xC000); got != 3 {
		t.Errorf("$C000 = $%02X; want bank 3 (last)", got)
	}
}

// CPUWrite to $8000-$FFFF sets the PRG bank.
func TestUxROM_BankSwitch(t *testing.T) {
	c, err := NewUxROM(fillUxromRom(t, 8))
	if err != nil {
		t.Fatalf("NewUxROM: %v", err)
	}
	c.CPUWrite(0x8000, 0x02)
	if got := c.CPURead(0x8000); got != 2 {
		t.Errorf("post-switch $8000 = $%02X; want bank 2", got)
	}
	// Switch again.
	c.CPUWrite(0xFFFF, 0x05)
	if got := c.CPURead(0x8000); got != 5 {
		t.Errorf("post-switch $8000 = $%02X; want bank 5", got)
	}
	// $C000 always last bank regardless of switch.
	if got := c.CPURead(0xC000); got != 7 {
		t.Errorf("$C000 = $%02X; want bank 7 (last, unchanged)", got)
	}
}

// CHR-RAM round-trips.
func TestUxROM_CHRRAMRoundTrip(t *testing.T) {
	c, err := NewUxROM(fillUxromRom(t, 2))
	if err != nil {
		t.Fatalf("NewUxROM: %v", err)
	}
	c.PPUWrite(0x0000, 0xAB)
	c.PPUWrite(0x1FFF, 0xCD)
	if got := c.PPURead(0x0000); got != 0xAB {
		t.Errorf("$0000 = $%02X; want $AB", got)
	}
	if got := c.PPURead(0x1FFF); got != 0xCD {
		t.Errorf("$1FFF = $%02X; want $CD", got)
	}
}

// Below-$8000 reads return open-bus 0; writes are no-ops.
func TestUxROM_BelowPRGUnmapped(t *testing.T) {
	c, err := NewUxROM(fillUxromRom(t, 2))
	if err != nil {
		t.Fatalf("NewUxROM: %v", err)
	}
	if got := c.CPURead(0x6000); got != 0 {
		t.Errorf("$6000 = $%02X; want open-bus 0", got)
	}
	c.CPUWrite(0x6000, 0xFF) // no-op
	if c.prgBank != 0 {
		t.Errorf("$6000 write touched prgBank: %d", c.prgBank)
	}
}

// cart.Open dispatches mapper=2 to UxROM.
func TestCartOpen_DispatchesUxROM(t *testing.T) {
	rom := fillUxromRom(t, 2)
	c, err := Open(rom)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := c.(*UxROM); !ok {
		t.Errorf("Open returned %T; want *UxROM", c)
	}
}

// Mirroring is pinned from the iNES header.
func TestUxROM_MirroringPinned(t *testing.T) {
	rom := fillUxromRom(t, 2)
	rom.Mirroring = nes.MirrorVertical
	c, err := NewUxROM(rom)
	if err != nil {
		t.Fatalf("NewUxROM: %v", err)
	}
	if c.Mirroring() != nes.MirrorVertical {
		t.Errorf("Mirroring = %s; want vertical", c.Mirroring())
	}
}
