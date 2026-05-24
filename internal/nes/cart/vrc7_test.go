package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func newVRC7Test(t *testing.T) *VRC7 {
	t.Helper()
	rom := &nes.ROM{
		Mapper:    85,
		PRG:       make([]byte, 256*1024),
		CHR:       make([]byte, 8*1024),
		Mirroring: nes.MirrorVertical,
	}
	for i := 0; i < len(rom.PRG)/(8*1024); i++ {
		rom.PRG[i*8*1024] = byte(i)
	}
	c, err := NewVRC7(rom)
	if err != nil {
		t.Fatalf("NewVRC7: %v", err)
	}
	return c
}

// $8000 / $8010 / $9000 select 8 KiB banks at $8000 / $A000 /
// $C000. $E000-$FFFF stays fixed on the last bank.
func TestVRC7_PRGBanks(t *testing.T) {
	c := newVRC7Test(t)
	c.CPUWrite(0x8000, 3)
	c.CPUWrite(0x8010, 7)
	c.CPUWrite(0x9000, 11)
	if got := c.CPURead(0x8000); got != 3 {
		t.Errorf("$8000 bank = %d; want 3", got)
	}
	if got := c.CPURead(0xA000); got != 7 {
		t.Errorf("$A000 bank = %d; want 7", got)
	}
	if got := c.CPURead(0xC000); got != 11 {
		t.Errorf("$C000 bank = %d; want 11", got)
	}
	if got := c.CPURead(0xE000); got != 31 {
		t.Errorf("$E000 fixed = %d; want 31 (last)", got)
	}
}

// Mirroring register at $E000 bits 0-1; WRAM-enable on bit 7.
func TestVRC7_MirroringAndWRAM(t *testing.T) {
	c := newVRC7Test(t)
	for _, tc := range []struct {
		v    byte
		want nes.Mirroring
	}{
		{0x80, nes.MirrorVertical},
		{0x81, nes.MirrorHorizontal},
		{0x82, nes.MirrorSingleLower},
		{0x83, nes.MirrorSingleUpper},
	} {
		c.CPUWrite(0xE000, tc.v)
		if got := c.Mirroring(); got != tc.want {
			t.Errorf("$E000=%02X → %v; want %v", tc.v, got, tc.want)
		}
	}
	// Disable WRAM (bit 7 clear) — reads return 0 + writes drop.
	c.CPUWrite(0xE000, 0x80)
	c.CPUWrite(0x6000, 0x42)
	c.CPUWrite(0xE000, 0x00)
	if got := c.CPURead(0x6000); got != 0 {
		t.Errorf("WRAM read with bit 7 clear = $%02X; want 0", got)
	}
	c.CPUWrite(0x6000, 0xFF) // dropped
	c.CPUWrite(0xE000, 0x80)
	if got := c.CPURead(0x6000); got != 0x42 {
		t.Errorf("WRAM persisted across enable/disable: $%02X; want $42", got)
	}
}

// VRC7 IRQ: $E010 latch, $F000 control, $F010 ack. Same shape as
// VRC4.
func TestVRC7_IRQ(t *testing.T) {
	c := newVRC7Test(t)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xE010, 0xFE)
	c.CPUWrite(0xF000, 0x06) // enable + CPU mode
	c.Tick(2)
	if sink.asserts != 1 {
		t.Errorf("asserts after 2 ticks = %d; want 1", sink.asserts)
	}
	c.CPUWrite(0xF010, 0) // ack
	if sink.clears != 1 {
		t.Errorf("clears after ack = %d; want 1", sink.clears)
	}
}

// Audio writes at $9010 / $9030 forward to the sink.
func TestVRC7_AudioForwarding(t *testing.T) {
	c := newVRC7Test(t)
	sink := &captureAudioSink{}
	c.SetAudioSink(sink)
	c.CPUWrite(0x9010, 0x07)
	c.CPUWrite(0x9030, 0xAB)
	if len(sink.writes) != 2 {
		t.Fatalf("writes = %d; want 2", len(sink.writes))
	}
	if sink.writes[0].addr != 0x9010 || sink.writes[0].val != 0x07 {
		t.Errorf("write 0 = %+v; want $9010=$07", sink.writes[0])
	}
	if sink.writes[1].addr != 0x9030 || sink.writes[1].val != 0xAB {
		t.Errorf("write 1 = %+v; want $9030=$AB", sink.writes[1])
	}
}

// cart.Open dispatches mapper 85 to VRC7.
func TestVRC7_OpenDispatch(t *testing.T) {
	rom := &nes.ROM{
		Mapper:    85,
		PRG:       make([]byte, 32*1024),
		CHR:       make([]byte, 8*1024),
		Mirroring: nes.MirrorVertical,
	}
	c, err := Open(rom)
	if err != nil {
		t.Fatalf("Open mapper 85: %v", err)
	}
	if _, ok := c.(*VRC7); !ok {
		t.Errorf("Open → %T; want *VRC7", c)
	}
}

// Save / restore round-trip.
func TestVRC7_SaveRestore(t *testing.T) {
	src := newVRC7Test(t)
	src.CPUWrite(0x8000, 3)
	src.CPUWrite(0x8010, 5)
	src.CPUWrite(0x9000, 7)
	src.CPUWrite(0xE000, 0x82) // single-lower + WRAM on
	src.CPUWrite(0xE010, 0xCD)
	src.CPUWrite(0xF000, 0x06)
	src.CPUWrite(0x6000, 0x99)
	s, err := SaveCart(src)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	dst := newVRC7Test(t)
	if err := LoadCart(dst, s); err != nil {
		t.Fatalf("load: %v", err)
	}
	if dst.prgBanks != [3]byte{3, 5, 7} {
		t.Errorf("PRG banks not restored: %v", dst.prgBanks)
	}
	if dst.mirroring != nes.MirrorSingleLower {
		t.Errorf("mirroring not restored: %v", dst.mirroring)
	}
	if dst.irqLatch != 0xCD || !dst.irqEnable {
		t.Errorf("IRQ state not restored")
	}
	if dst.prgRAM[0] != 0x99 {
		t.Errorf("PRG-RAM not restored")
	}
}
