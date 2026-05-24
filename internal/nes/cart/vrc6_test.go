package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func newVRC6Test(t *testing.T, mapper uint16) *VRC6 {
	t.Helper()
	rom := &nes.ROM{
		Mapper:    mapper,
		PRG:       make([]byte, 256*1024),
		CHR:       make([]byte, 8*1024),
		Mirroring: nes.MirrorVertical,
	}
	for i := 0; i < len(rom.PRG)/(8*1024); i++ {
		rom.PRG[i*8*1024] = byte(i)
	}
	c, err := NewVRC6(rom)
	if err != nil {
		t.Fatalf("NewVRC6: %v", err)
	}
	return c
}

// $8000 bank reg selects a 16 KiB window — that's bank-index ×2 in
// 8 KiB units.
func TestVRC6_PRG16Bank(t *testing.T) {
	c := newVRC6Test(t, 24)
	c.CPUWrite(0x8000, 5) // bank 5 of 16 KiB → 10 + 11 in 8 KiB units
	if got := c.CPURead(0x8000); got != 10 {
		t.Errorf("$8000 = %d; want 10", got)
	}
	// 8 KiB later still in the same 16 KiB window:
	if got := c.CPURead(0xA000); got != 11 {
		t.Errorf("$A000 = %d; want 11", got)
	}
}

// $C000 bank reg selects the 8 KiB window at $C000-$DFFF; $E000-
// $FFFF stays fixed on the last bank.
func TestVRC6_PRG8BankAndFixedTail(t *testing.T) {
	c := newVRC6Test(t, 24)
	c.CPUWrite(0xC000, 7)
	if got := c.CPURead(0xC000); got != 7 {
		t.Errorf("$C000 = %d; want 7", got)
	}
	if got := c.CPURead(0xE000); got != 31 {
		t.Errorf("$E000 fixed = %d; want 31 (last)", got)
	}
}

// $B003 register controls mirroring — bits 2-3.
func TestVRC6_Mirroring(t *testing.T) {
	c := newVRC6Test(t, 24)
	for _, tc := range []struct {
		v    byte
		want nes.Mirroring
	}{
		{0x00, nes.MirrorVertical},
		{0x04, nes.MirrorHorizontal},
		{0x08, nes.MirrorSingleLower},
		{0x0C, nes.MirrorSingleUpper},
	} {
		c.CPUWrite(0xB003, tc.v)
		if got := c.Mirroring(); got != tc.want {
			t.Errorf("$B003=%02X → %v; want %v", tc.v, got, tc.want)
		}
	}
}

// IRQ counter (same shape as VRC4): CPU mode, $FE latch, 2 ticks
// to underflow.
func TestVRC6_IRQ(t *testing.T) {
	c := newVRC6Test(t, 24)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xF000, 0xFE)
	c.CPUWrite(0xF001, 0x06) // enable + CPU mode
	c.Tick(2)
	if sink.asserts != 1 {
		t.Errorf("asserts after 2 ticks = %d; want 1", sink.asserts)
	}
	c.CPUWrite(0xF002, 0) // ack
	if sink.clears != 1 {
		t.Errorf("clears after ack = %d; want 1", sink.clears)
	}
}

// Audio writes get forwarded to the sink with logical addresses.
type captureAudioSink struct{ writes []writeRecord }

type writeRecord struct {
	addr uint16
	val  byte
}

func (s *captureAudioSink) Write(addr uint16, v byte) {
	s.writes = append(s.writes, writeRecord{addr, v})
}

func TestVRC6_AudioForwarding(t *testing.T) {
	c := newVRC6Test(t, 24)
	sink := &captureAudioSink{}
	c.SetAudioSink(sink)
	c.CPUWrite(0x9000, 0x42) // pulse 1 vol/duty
	c.CPUWrite(0xA001, 0x55) // pulse 2 period low
	c.CPUWrite(0xB002, 0x88) // sawtooth period high + enable
	// $B003 is the banking byte, NOT forwarded.
	c.CPUWrite(0xB003, 0x0C)
	if len(sink.writes) != 3 {
		t.Fatalf("forwarded writes = %d; want 3", len(sink.writes))
	}
	if sink.writes[0].addr != 0x9000 || sink.writes[0].val != 0x42 {
		t.Errorf("write 0 = %+v; want $9000=$42", sink.writes[0])
	}
	if sink.writes[2].addr != 0xB002 || sink.writes[2].val != 0x88 {
		t.Errorf("write 2 = %+v; want $B002=$88", sink.writes[2])
	}
}

// VRC6b (mapper 26) swaps sub-bits — the same physical address
// hits a different sub-register.
func TestVRC6b_SubBitsSwapped(t *testing.T) {
	a := newVRC6Test(t, 24) // VRC6a
	b := newVRC6Test(t, 26) // VRC6b
	// $D001 on VRC6a → sub 1 (CHR bank 1). On VRC6b → sub 2 (CHR bank 2).
	a.CPUWrite(0xD001, 0xAA)
	b.CPUWrite(0xD001, 0xBB)
	if a.chrBanks[1] != 0xAA {
		t.Errorf("VRC6a chrBanks[1] = $%02X; want $AA", a.chrBanks[1])
	}
	if b.chrBanks[2] != 0xBB {
		t.Errorf("VRC6b chrBanks[2] = $%02X; want $BB", b.chrBanks[2])
	}
}

// cart.Open dispatches mappers 24 + 26.
func TestVRC6_OpenDispatch(t *testing.T) {
	for _, m := range []uint16{24, 26} {
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
		if _, ok := c.(*VRC6); !ok {
			t.Errorf("mapper %d → %T; want *VRC6", m, c)
		}
	}
}

// Save / restore round-trip.
func TestVRC6_SaveRestore(t *testing.T) {
	src := newVRC6Test(t, 24)
	src.CPUWrite(0x8000, 3)
	src.CPUWrite(0xC000, 7)
	src.CPUWrite(0xB003, 0x08) // single-screen lower
	src.CPUWrite(0xF000, 0xCD)
	src.CPUWrite(0xF001, 0x06)
	src.CPUWrite(0x6000, 0x77)
	s, err := SaveCart(src)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	dst := newVRC6Test(t, 24)
	if err := LoadCart(dst, s); err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.prgBank16 != 3 || dst.prgBank8 != 7 {
		t.Errorf("PRG banks not restored: 16=%d 8=%d", dst.prgBank16, dst.prgBank8)
	}
	if dst.mirroring != nes.MirrorSingleLower {
		t.Errorf("mirroring not restored: %v", dst.mirroring)
	}
	if dst.irqLatch != 0xCD || !dst.irqEnable {
		t.Errorf("IRQ state not restored")
	}
	if dst.prgRAM[0] != 0x77 {
		t.Errorf("PRG-RAM[0] not restored")
	}
}
