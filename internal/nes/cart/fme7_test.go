package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func newFME7Test(t *testing.T) *FME7 {
	t.Helper()
	rom := &nes.ROM{
		Mapper:    69,
		PRG:       make([]byte, 256*1024), // 32 × 8 KiB banks
		CHR:       make([]byte, 8*1024),   // 8 × 1 KiB banks
		Mirroring: nes.MirrorVertical,
	}
	// Stamp each 8 KiB PRG bank with its index so reads tell us the
	// active bank: byte 0 of bank N is N.
	for i := 0; i < len(rom.PRG)/(8*1024); i++ {
		rom.PRG[i*8*1024] = byte(i)
	}
	c, err := NewFME7(rom)
	if err != nil {
		t.Fatalf("NewFME7: %v", err)
	}
	return c
}

func setRegister(c *FME7, cmd, param byte) {
	c.CPUWrite(0x8000, cmd)
	c.CPUWrite(0xA000, param)
}

// PRG bank registers commands 9 / 10 / 11 select the bank visible
// at $8000 / $A000 / $C000 respectively. $E000 stays fixed on the
// last bank.
func TestFME7_PRGBankRegisters(t *testing.T) {
	c := newFME7Test(t)
	setRegister(c, 9, 5)
	setRegister(c, 10, 7)
	setRegister(c, 11, 11)
	if got := c.CPURead(0x8000); got != 5 {
		t.Errorf("$8000 bank = %d; want 5", got)
	}
	if got := c.CPURead(0xA000); got != 7 {
		t.Errorf("$A000 bank = %d; want 7", got)
	}
	if got := c.CPURead(0xC000); got != 11 {
		t.Errorf("$C000 bank = %d; want 11", got)
	}
	if got := c.CPURead(0xE000); got != 31 {
		t.Errorf("$E000 fixed bank = %d; want 31 (last)", got)
	}
}

// Command 12 cycles the four mirroring modes.
func TestFME7_MirroringControl(t *testing.T) {
	c := newFME7Test(t)
	for _, tc := range []struct {
		param byte
		want  nes.Mirroring
	}{
		{0, nes.MirrorVertical},
		{1, nes.MirrorHorizontal},
		{2, nes.MirrorSingleLower},
		{3, nes.MirrorSingleUpper},
	} {
		setRegister(c, 12, tc.param)
		if got := c.Mirroring(); got != tc.want {
			t.Errorf("param=%d mirroring = %v; want %v", tc.param, got, tc.want)
		}
	}
}

// fakeIRQSink records source assert / clear events.
type fakeIRQSink struct {
	asserts int
	clears  int
}

func (f *fakeIRQSink) AssertIRQSource(string) { f.asserts++ }
func (f *fakeIRQSink) ClearIRQSource(string)  { f.clears++ }

// IRQ counter decrements per Tick(1) when counter-enable is set.
// On underflow with IRQ-enable set, the sink is asserted.
func TestFME7_IRQ_UnderflowAsserts(t *testing.T) {
	c := newFME7Test(t)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)

	// Counter = 3, IRQ-enable + counter-enable on.
	setRegister(c, 14, 3) // low
	setRegister(c, 15, 0) // high
	setRegister(c, 13, 0x81)

	// Three ticks bring it to 0 without an underflow yet.
	c.Tick(3)
	if sink.asserts != 0 {
		t.Errorf("premature assert: %d", sink.asserts)
	}
	// Fourth tick: counter pre-decrements from 0 to $FFFF and fires.
	c.Tick(1)
	if sink.asserts != 1 {
		t.Errorf("post-underflow asserts = %d; want 1", sink.asserts)
	}
	if c.irqCounter != 0xFFFF {
		t.Errorf("counter = $%04X; want $FFFF", c.irqCounter)
	}

	// Writing command 13 acks.
	setRegister(c, 13, 0x80)
	if sink.clears != 1 {
		t.Errorf("post-ack clears = %d; want 1", sink.clears)
	}
	if c.irqPending {
		t.Errorf("irqPending should be false after ack")
	}
}

// IRQ stays silent when counter-enable bit is off, even with a low
// counter value.
func TestFME7_IRQ_CounterDisabled(t *testing.T) {
	c := newFME7Test(t)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	setRegister(c, 14, 1)
	setRegister(c, 15, 0)
	setRegister(c, 13, 0x01) // IRQ enable on, counter disable
	c.Tick(100)
	if sink.asserts != 0 {
		t.Errorf("asserted with counter disabled: %d", sink.asserts)
	}
}

// CHR banks 0-7 surface as the 8 × 1 KiB windows on the PPU bus.
func TestFME7_CHRBanks(t *testing.T) {
	c := newFME7Test(t)
	// Stamp CHR data: byte at offset 1024*N is N. Re-load over the
	// 8 KiB CHR-RAM we got from newFME7Test.
	for i := range 8 {
		c.chr[i*1024] = byte(i + 0x10)
	}
	for i := byte(0); i < 8; i++ {
		setRegister(c, i, i) // bank N → window N
	}
	for i := uint16(0); i < 8; i++ {
		addr := i << 10
		want := byte(i + 0x10)
		if got := c.PPURead(addr); got != want {
			t.Errorf("PPU[$%04X] = $%02X; want $%02X", addr, got, want)
		}
	}
}

// cart.Open dispatches mapper=69 to NewFME7.
func TestFME7_OpenDispatch(t *testing.T) {
	rom := &nes.ROM{
		Mapper:    69,
		PRG:       make([]byte, 32*1024),
		CHR:       make([]byte, 8*1024),
		Mirroring: nes.MirrorVertical,
	}
	c, err := Open(rom)
	if err != nil {
		t.Fatalf("Open mapper 69: %v", err)
	}
	if _, ok := c.(*FME7); !ok {
		t.Errorf("Open returned %T; want *FME7", c)
	}
}

// Save / restore round-trip preserves every mapper register +
// IRQ state + PRG-RAM.
func TestFME7_SaveRestore(t *testing.T) {
	src := newFME7Test(t)
	setRegister(src, 9, 13)
	setRegister(src, 10, 5)
	setRegister(src, 12, 2) // single-screen-lower
	setRegister(src, 14, 0xAB)
	setRegister(src, 15, 0xCD)
	setRegister(src, 13, 0x81)
	src.CPUWrite(0x6000, 0x42) // would need PRG-RAM enabled; not enabled here so no-op
	// Actually enable PRG-RAM mode to land a write.
	setRegister(src, 8, 0xC0)
	src.CPUWrite(0x6010, 0x99)

	s, err := SaveCart(src)
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	dst := newFME7Test(t)
	if err := LoadCart(dst, s); err != nil {
		t.Fatalf("load: %v", err)
	}

	if dst.prgBanks != [3]byte{13, 5, 0} {
		t.Errorf("prgBanks not restored: %v", dst.prgBanks)
	}
	if dst.mirroring != nes.MirrorSingleLower {
		t.Errorf("mirroring not restored: %v", dst.mirroring)
	}
	if dst.irqCounter != 0xCDAB {
		t.Errorf("irqCounter = $%04X; want $CDAB", dst.irqCounter)
	}
	if !dst.irqCountEnable || !dst.irqEnable {
		t.Errorf("IRQ enable bits not restored")
	}
	if dst.prgRAM[0x10] != 0x99 {
		t.Errorf("PRG-RAM byte not restored: %02X", dst.prgRAM[0x10])
	}
}
