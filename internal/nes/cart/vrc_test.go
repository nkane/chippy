package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func newVRCTest(t *testing.T, mapper uint16, sub uint8) *VRC {
	t.Helper()
	rom := &nes.ROM{
		Mapper:    mapper,
		SubMapper: sub,
		PRG:       make([]byte, 256*1024), // 32 × 8 KiB
		CHR:       make([]byte, 8*1024),   // 8 × 1 KiB
		Mirroring: nes.MirrorVertical,
	}
	// Stamp each PRG bank with its index so reads tell us the
	// active bank: byte 0 of bank N == N.
	for i := 0; i < len(rom.PRG)/(8*1024); i++ {
		rom.PRG[i*8*1024] = byte(i)
	}
	c, err := NewVRC(rom)
	if err != nil {
		t.Fatalf("NewVRC: %v", err)
	}
	return c
}

// Mapper 23 default = VRC4f. PRG bank registers $8000 + $A000
// select 8 KiB banks; $C000 + $E000 are fixed (second-to-last
// + last) in PRG mode 0.
func TestVRC_Mapper23_PRGBanksDefaultMode(t *testing.T) {
	c := newVRCTest(t, 23, 0)
	c.CPUWrite(0x8000, 7) // $8000 → bank 7
	c.CPUWrite(0xA000, 5) // $A000 → bank 5
	if got := c.CPURead(0x8000); got != 7 {
		t.Errorf("$8000 bank = %d; want 7", got)
	}
	if got := c.CPURead(0xA000); got != 5 {
		t.Errorf("$A000 bank = %d; want 5", got)
	}
	totalBanks := 32
	if got := c.CPURead(0xC000); got != byte(totalBanks-2) {
		t.Errorf("$C000 fixed = %d; want %d", got, totalBanks-2)
	}
	if got := c.CPURead(0xE000); got != byte(totalBanks-1) {
		t.Errorf("$E000 fixed = %d; want %d", got, totalBanks-1)
	}
}

// PRG mode 1 swaps $8000 with $C000: $8000 fixed second-to-last,
// $C000 takes the prgBank0 value.
func TestVRC_Mapper23_PRGModeSwap(t *testing.T) {
	c := newVRCTest(t, 23, 0)
	c.CPUWrite(0x8000, 9)
	// $9002 in sub-mapper-0 (VRC4f, A0+A1 routing) → sub=2 in $9000
	// class → PRG mode bit. Bit 1 set = mode 1.
	c.CPUWrite(0x9002, 0x02)
	totalBanks := 32
	if got := c.CPURead(0x8000); got != byte(totalBanks-2) {
		t.Errorf("$8000 in mode 1 = %d; want %d", got, totalBanks-2)
	}
	if got := c.CPURead(0xC000); got != 9 {
		t.Errorf("$C000 in mode 1 = %d; want 9 (prgBank0)", got)
	}
}

// Mirroring control via $9000 / $9001 (sub 0 + 1).
func TestVRC_Mapper23_Mirroring(t *testing.T) {
	c := newVRCTest(t, 23, 0)
	for _, tc := range []struct {
		v    byte
		want nes.Mirroring
	}{
		{0, nes.MirrorVertical},
		{1, nes.MirrorHorizontal},
		{2, nes.MirrorSingleLower},
		{3, nes.MirrorSingleUpper},
	} {
		c.CPUWrite(0x9000, tc.v)
		if got := c.Mirroring(); got != tc.want {
			t.Errorf("$9000=%d → %v; want %v", tc.v, got, tc.want)
		}
	}
}

// VRC4 IRQ: CPU mode + counter pre-loaded near $FF fires after
// one tick per counter increment.
func TestVRC_VRC4_CPUMode_IRQ(t *testing.T) {
	c := newVRCTest(t, 23, 0)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	// Latch = $FE: $F000 sub 0 = low nibble; $F001 sub 1 = high.
	c.CPUWrite(0xF000, 0x0E)
	c.CPUWrite(0xF001, 0x0F)
	// Control $F002 sub 2: bit 1 = enable, bit 2 = mode (1 = CPU).
	c.CPUWrite(0xF002, 0x06)
	// Counter loaded with latch = $FE. Need 2 ticks to go $FE → $FF → reload + fire.
	c.Tick(2)
	if sink.asserts != 1 {
		t.Errorf("asserts after 2 ticks = %d; want 1", sink.asserts)
	}
	// Ack via $F003.
	c.CPUWrite(0xF003, 0)
	if sink.clears != 1 {
		t.Errorf("clears after ack = %d; want 1", sink.clears)
	}
}

// VRC4 scanline mode: 341 CPU cycles per counter tick. Counter
// pre-set to $FF, so one prescaler underflow fires immediately.
func TestVRC_VRC4_ScanlineMode_IRQ(t *testing.T) {
	c := newVRCTest(t, 23, 0)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xF000, 0x0F)
	c.CPUWrite(0xF001, 0x0F)
	// Scanline mode = bit 2 clear, enable = bit 1.
	c.CPUWrite(0xF002, 0x02)
	// 341 cycles → one counter tick from $FF → reload + fire.
	c.Tick(341)
	if sink.asserts != 1 {
		t.Errorf("scanline-mode asserts after 341 ticks = %d; want 1", sink.asserts)
	}
}

// Mapper 22 (VRC2a): CHR bank values get pre-shifted right by 1
// before lookup because the chip only routes the upper 7 bits.
func TestVRC_Mapper22_VRC2a_CHRHalveBank(t *testing.T) {
	c := newVRCTest(t, 22, 0)
	// Stamp CHR bank 1 (offset $0400 in the 8 KiB CHR) so we can
	// detect when bank index 2 (which halves to 1) routes us here.
	c.chr[0x0400] = 0xAB
	// VRC2a $B000 register: sub 0 = bank 0 low nibble. Write 2 →
	// halved to bank 1.
	c.CPUWrite(0xB000, 2)
	if got := c.PPURead(0x0000); got != 0xAB {
		t.Errorf("VRC2a halved CHR read = $%02X; want $AB", got)
	}
}

// VRC2 (mapper 23 sub 3) ignores writes to $F000-$FFFF. The IRQ
// counter never fires regardless of how many Ticks elapse.
func TestVRC_VRC2_NoIRQ(t *testing.T) {
	c := newVRCTest(t, 23, 3) // sub 3 = VRC2b
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xF000, 0x0F)
	c.CPUWrite(0xF002, 0x06) // would enable CPU-mode IRQ on VRC4
	c.Tick(1000)
	if sink.asserts != 0 {
		t.Errorf("VRC2 asserted IRQ: %d", sink.asserts)
	}
}

// cart.Open dispatches the VRC mapper numbers.
func TestVRC_OpenDispatch(t *testing.T) {
	for _, m := range []uint16{21, 22, 23, 25} {
		rom := &nes.ROM{
			Mapper:    m,
			PRG:       make([]byte, 32*1024),
			CHR:       make([]byte, 8*1024),
			Mirroring: nes.MirrorVertical,
		}
		c, err := Open(rom)
		if err != nil {
			t.Errorf("Open mapper %d: %v", m, err)
			continue
		}
		if _, ok := c.(*VRC); !ok {
			t.Errorf("mapper %d → %T; want *VRC", m, c)
		}
	}
}

// Save / restore round-trip preserves every register + IRQ state +
// PRG-RAM.
func TestVRC_SaveRestore(t *testing.T) {
	src := newVRCTest(t, 23, 0)
	src.CPUWrite(0x8000, 3)
	src.CPUWrite(0xA000, 7)
	src.CPUWrite(0x9000, 2) // single-screen-lower
	src.CPUWrite(0xF000, 0x05)
	src.CPUWrite(0xF001, 0x0A)
	src.CPUWrite(0xF002, 0x06) // CPU-mode IRQ
	src.CPUWrite(0x6000, 0x42)

	s, err := SaveCart(src)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	dst := newVRCTest(t, 23, 0)
	if err := LoadCart(dst, s); err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.prgBank0 != 3 || dst.prgBank1 != 7 {
		t.Errorf("PRG banks not restored: %d / %d", dst.prgBank0, dst.prgBank1)
	}
	if dst.mirroring != nes.MirrorSingleLower {
		t.Errorf("mirroring not restored: %v", dst.mirroring)
	}
	if dst.irqLatch != 0xA5 {
		t.Errorf("IRQ latch = $%02X; want $A5", dst.irqLatch)
	}
	if !dst.irqEnable || dst.irqMode != 1 {
		t.Errorf("IRQ control not restored: enable=%v mode=%d", dst.irqEnable, dst.irqMode)
	}
	if dst.prgRAM[0] != 0x42 {
		t.Errorf("PRG-RAM[0] = $%02X; want $42", dst.prgRAM[0])
	}
}
