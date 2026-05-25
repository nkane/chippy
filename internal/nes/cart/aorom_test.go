package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func newAOROMTest(t *testing.T, banks int) *AOROM {
	t.Helper()
	prg := make([]byte, banks*32*1024)
	// Stamp byte 0 of each 32 KiB bank with its index.
	for i := 0; i < banks; i++ {
		prg[i*32*1024] = byte(i)
	}
	c, err := NewAOROM(&nes.ROM{Mapper: 7, PRG: prg, Mirroring: nes.MirrorSingleLower})
	if err != nil {
		t.Fatalf("NewAOROM: %v", err)
	}
	return c
}

// $8000 writes select the 32 KiB bank (bits 0-2).
func TestAOROM_BankSwitch(t *testing.T) {
	c := newAOROMTest(t, 8)
	if got := c.CPURead(0x8000); got != 0 {
		t.Errorf("power-on bank = %d; want 0", got)
	}
	c.CPUWrite(0x8000, 0x05)
	if got := c.CPURead(0x8000); got != 5 {
		t.Errorf("post-switch bank = %d; want 5", got)
	}
	// The window is a full 32 KiB — $FFFF still reads from the same bank.
	c.CPUWrite(0xABCD, 0x03)
	if got := c.CPURead(0x8000); got != 3 {
		t.Errorf("bank after write via $ABCD = %d; want 3", got)
	}
}

// Bit 4 flips single-screen mirroring between the two nametables.
func TestAOROM_SingleScreenToggle(t *testing.T) {
	c := newAOROMTest(t, 2)
	c.CPUWrite(0x8000, 0x00) // bit 4 clear → lower
	if c.Mirroring() != nes.MirrorSingleLower {
		t.Errorf("bit4=0 → %v; want SingleLower", c.Mirroring())
	}
	c.CPUWrite(0x8000, 0x10) // bit 4 set → upper
	if c.Mirroring() != nes.MirrorSingleUpper {
		t.Errorf("bit4=1 → %v; want SingleUpper", c.Mirroring())
	}
}

// CHR-RAM round-trips through the PPU bus.
func TestAOROM_CHRRAM(t *testing.T) {
	c := newAOROMTest(t, 1)
	c.PPUWrite(0x0123, 0x9C)
	if got := c.PPURead(0x0123); got != 0x9C {
		t.Errorf("CHR-RAM read = $%02X; want $9C", got)
	}
}

// cart.Open dispatches mapper 7 to AOROM.
func TestAOROM_OpenDispatch(t *testing.T) {
	c, err := Open(&nes.ROM{Mapper: 7, PRG: make([]byte, 32*1024), Mirroring: nes.MirrorSingleLower})
	if err != nil {
		t.Fatalf("Open mapper 7: %v", err)
	}
	if _, ok := c.(*AOROM); !ok {
		t.Errorf("Open → %T; want *AOROM", c)
	}
}

// Save / restore round-trips bank + mirroring + CHR-RAM.
func TestAOROM_SaveRestore(t *testing.T) {
	src := newAOROMTest(t, 4)
	src.CPUWrite(0x8000, 0x13) // bank 3 + bit 4 (upper)
	src.PPUWrite(0x0010, 0x77)
	s, err := SaveCart(src)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	dst := newAOROMTest(t, 4)
	if err := LoadCart(dst, s); err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.prgBank != 3 {
		t.Errorf("prgBank = %d; want 3", dst.prgBank)
	}
	if dst.mirroring != nes.MirrorSingleUpper {
		t.Errorf("mirroring = %v; want SingleUpper", dst.mirroring)
	}
	if dst.PPURead(0x0010) != 0x77 {
		t.Errorf("CHR-RAM not restored")
	}
}
