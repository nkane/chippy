package cart

import (
	"errors"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// fillROM constructs a *nes.ROM with PRG / CHR filled with caller-
// supplied patterns. Bypasses Parse() so cart tests don't depend on
// the iNES header layout.
func fillROM(prg, chr []byte, mir nes.Mirroring) *nes.ROM {
	return &nes.ROM{Mapper: 0, Mirroring: mir, PRG: prg, CHR: chr}
}

// 32 KiB NROM: $8000-$FFFF maps directly to the bank, no mirroring.
func TestNROM_32KiB_DirectMap(t *testing.T) {
	prg := make([]byte, 32*1024)
	for i := range prg {
		prg[i] = byte(i & 0xFF)
	}
	chr := make([]byte, 8*1024)
	c, err := NewNROM(fillROM(prg, chr, nes.MirrorHorizontal))
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	for _, addr := range []uint16{0x8000, 0xA000, 0xC000, 0xFFFF} {
		got := c.CPURead(addr)
		want := byte((int(addr) - 0x8000) & 0xFF)
		if got != want {
			t.Errorf("32K NROM CPURead($%04X) = $%02X; want $%02X", addr, got, want)
		}
	}
}

// 16 KiB NROM: $8000-$BFFF maps to the bank; $C000-$FFFF mirrors it.
func TestNROM_16KiB_MirrorsTopBank(t *testing.T) {
	prg := make([]byte, 16*1024)
	for i := range prg {
		prg[i] = byte(i ^ 0x55)
	}
	chr := make([]byte, 8*1024)
	c, _ := NewNROM(fillROM(prg, chr, nes.MirrorHorizontal))
	for _, off := range []uint16{0x0000, 0x1000, 0x2345, 0x3FFF} {
		low := c.CPURead(0x8000 + off)
		high := c.CPURead(0xC000 + off)
		if low != high {
			t.Errorf("16K NROM: $%04X=$%02X vs mirror $%04X=$%02X",
				0x8000+off, low, 0xC000+off, high)
		}
	}
}

// Reads below $8000 are unmapped on NROM — return 0 deterministically.
func TestNROM_UnmappedReadBelow8000(t *testing.T) {
	c, _ := NewNROM(fillROM(make([]byte, 32*1024), make([]byte, 8*1024), nes.MirrorHorizontal))
	for _, addr := range []uint16{0x0000, 0x4020, 0x6000, 0x7FFF} {
		if got := c.CPURead(addr); got != 0 {
			t.Errorf("unmapped CPURead($%04X) = $%02X; want 0", addr, got)
		}
	}
}

// PRG writes are silent no-ops — value at the address must not change.
func TestNROM_PRGWriteIsNoOp(t *testing.T) {
	prg := make([]byte, 32*1024)
	prg[0] = 0x42
	c, _ := NewNROM(fillROM(prg, make([]byte, 8*1024), nes.MirrorHorizontal))
	c.CPUWrite(0x8000, 0xFF)
	if got := c.CPURead(0x8000); got != 0x42 {
		t.Fatalf("CPUWrite should be no-op; got $%02X want $42", got)
	}
}

// PPU side: CHR-ROM cart accepts reads, drops writes.
func TestNROM_CHRROM_ReadOnly(t *testing.T) {
	chr := make([]byte, 8*1024)
	for i := range chr {
		chr[i] = byte(i & 0xFF)
	}
	c, _ := NewNROM(fillROM(make([]byte, 32*1024), chr, nes.MirrorHorizontal))
	if got := c.PPURead(0x0042); got != 0x42 {
		t.Errorf("PPURead want $42; got $%02X", got)
	}
	c.PPUWrite(0x0042, 0xFF)
	if got := c.PPURead(0x0042); got != 0x42 {
		t.Errorf("CHR-ROM PPUWrite should be no-op; got $%02X", got)
	}
}

// CHR-RAM variant (rom.CHR == nil → cart allocates 8 KiB). PPU writes
// land; subsequent reads see the new value.
func TestNROM_CHRRAM_WriteRoundtrip(t *testing.T) {
	c, err := NewNROM(fillROM(make([]byte, 32*1024), nil, nes.MirrorVertical))
	if err != nil {
		t.Fatalf("ctor: %v", err)
	}
	c.PPUWrite(0x1234, 0xAB)
	if got := c.PPURead(0x1234); got != 0xAB {
		t.Errorf("CHR-RAM round-trip: got $%02X want $AB", got)
	}
	// Mirroring still propagates from the ROM header.
	if c.Mirroring() != nes.MirrorVertical {
		t.Errorf("mirroring = %s; want vertical", c.Mirroring())
	}
}

// Reads/writes above $1FFF on the PPU bus belong to nametables /
// palettes, not the cart. NROM ignores them.
func TestNROM_PPUAboveBank_Ignored(t *testing.T) {
	c, _ := NewNROM(fillROM(make([]byte, 32*1024), make([]byte, 8*1024), nes.MirrorHorizontal))
	if got := c.PPURead(0x3000); got != 0 {
		t.Errorf("PPURead($3000) = $%02X; want 0", got)
	}
	c.PPUWrite(0x3000, 0xFF) // no-op
}

// Constructor rejects PRG sizes outside the 16 / 32 KiB envelope.
func TestNROM_RejectsBadPRGSize(t *testing.T) {
	for _, size := range []int{0, 8 * 1024, 24 * 1024, 48 * 1024} {
		_, err := NewNROM(fillROM(make([]byte, size), make([]byte, 8*1024), nes.MirrorHorizontal))
		if err == nil {
			t.Errorf("PRG=%d KiB should reject", size/1024)
		}
	}
}

// Open() dispatches on Mapper. NROM works; everything else returns an
// explicit "unsupported in v0.1" error.
func TestOpen_DispatchByMapper(t *testing.T) {
	rom := fillROM(make([]byte, 32*1024), make([]byte, 8*1024), nes.MirrorHorizontal)
	rom.Mapper = 0
	cart, err := Open(rom)
	if err != nil {
		t.Fatalf("Open(NROM): %v", err)
	}
	if _, ok := cart.(*NROM); !ok {
		t.Errorf("Open should return *NROM; got %T", cart)
	}

	rom.Mapper = 4 // MMC3
	_, err = Open(rom)
	if err == nil {
		t.Fatalf("MMC3 should return unsupported error")
	}
	if !strings.Contains(err.Error(), "unsupported mapper 4") {
		t.Errorf("error should name the mapper number; got %q", err.Error())
	}
	// Sentinel-style errors not yet defined — string-contains is enough
	// for v0.1.
	_ = errors.New // appease the import even if Sentinel arrives later
}
