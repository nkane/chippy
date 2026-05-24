package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// fillMMC3Rom builds an MMC3 ROM with `prgBanks` 8 KiB PRG banks
// and `chrBanks` 1 KiB CHR banks. Each PRG bank's first byte =
// bank number for identification.
func fillMMC3Rom(t *testing.T, prgBanks, chrBanks int) *nes.ROM {
	t.Helper()
	prg := make([]byte, prgBanks*8*1024)
	for i := range prgBanks {
		prg[i*8*1024] = byte(i)
	}
	chr := make([]byte, chrBanks*1024)
	for i := range chrBanks {
		chr[i*1024] = byte(i)
	}
	return &nes.ROM{Mapper: 4, PRG: prg, CHR: chr}
}

// commitMMC3 helper: bank-select + bank-data write.
func setBankRegister(c *MMC3, reg, value byte) {
	c.CPUWrite(0x8000, reg)
	c.CPUWrite(0x8001, value)
}

// Power-on PRG mode 0: last 16 KiB fixed at $C000/$E000.
func TestMMC3_PowerOnPRGMode0(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 8, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	if got := c.CPURead(0xC000); got != 6 {
		t.Errorf("$C000 = $%02X; want bank 6 (N-2)", got)
	}
	if got := c.CPURead(0xE000); got != 7 {
		t.Errorf("$E000 = $%02X; want bank 7 (last)", got)
	}
}

// R6 controls $8000 in PRG mode 0. Switch + verify.
func TestMMC3_PRGMode0_R6SwitchesBase(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 8, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	setBankRegister(c, 6, 3) // R6 = bank 3
	if got := c.CPURead(0x8000); got != 3 {
		t.Errorf("$8000 = $%02X; want bank 3", got)
	}
}

// PRG mode 1: $8000 = fixed N-2, $C000 = R6. Bank-select byte
// must carry the mode bit + reg index together (real silicon).
func TestMMC3_PRGMode1_Swap(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 8, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	// Mode 1 (bit 6) + reg 6 → $46; data write goes to R6.
	c.CPUWrite(0x8000, 0x46)
	c.CPUWrite(0x8001, 3)
	if got := c.CPURead(0x8000); got != 6 {
		t.Errorf("mode 1 $8000 = $%02X; want N-2 = 6", got)
	}
	if got := c.CPURead(0xC000); got != 3 {
		t.Errorf("mode 1 $C000 = $%02X; want R6 = 3", got)
	}
}

// CHR mode 0 layout: $0000 = R0 (2 KiB), $1000 = R2..R5 (1 KiB).
func TestMMC3_CHRMode0Layout(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 4, 16))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	setBankRegister(c, 0, 4)  // R0 = 4 (2 KiB bank)
	setBankRegister(c, 2, 10) // R2 = 10 (1 KiB bank)
	if got := c.PPURead(0x0000); got != 4 {
		t.Errorf("CHR $0000 (mode 0) = $%02X; want bank 4 from R0", got)
	}
	if got := c.PPURead(0x1000); got != 10 {
		t.Errorf("CHR $1000 (mode 0) = $%02X; want bank 10 from R2", got)
	}
}

// CHR mode 1: layout inverts (low half 1 KiB, high half 2 KiB).
// Each bank-data write needs CHR mode 1 bit ORed with the reg idx.
func TestMMC3_CHRMode1Inverts(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 4, 16))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	c.CPUWrite(0x8000, 0x80) // CHR mode 1 + reg 0
	c.CPUWrite(0x8001, 4)    // R0 = 4
	c.CPUWrite(0x8000, 0x82) // CHR mode 1 + reg 2
	c.CPUWrite(0x8001, 10)   // R2 = 10
	if got := c.PPURead(0x0000); got != 10 {
		t.Errorf("CHR $0000 (mode 1) = $%02X; want R2 = 10", got)
	}
	if got := c.PPURead(0x1000); got != 4 {
		t.Errorf("CHR $1000 (mode 1) = $%02X; want R0 = 4", got)
	}
}

// $A000 bit 0 sets mirroring at runtime.
func TestMMC3_MirroringRuntime(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 4, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	c.CPUWrite(0xA000, 0x01) // horizontal
	if c.Mirroring() != nes.MirrorHorizontal {
		t.Errorf("mirror after $A000=1 = %s; want horizontal", c.Mirroring())
	}
	c.CPUWrite(0xA000, 0x00) // vertical
	if c.Mirroring() != nes.MirrorVertical {
		t.Errorf("mirror after $A000=0 = %s; want vertical", c.Mirroring())
	}
}

// PRG-RAM at $6000-$7FFF round-trips.
func TestMMC3_PRGRAMRoundTrip(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 4, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	c.CPUWrite(0x6000, 0xAB)
	if got := c.CPURead(0x6000); got != 0xAB {
		t.Errorf("PRG-RAM round-trip failed: $%02X", got)
	}
}

// fakeIRQSink tracks asserts/clears for the named source.
type mmc3FakeSink struct {
	asserts int
	clears  int
}

func (s *mmc3FakeSink) AssertIRQSource(src string) {
	if src == mmc3IRQSource {
		s.asserts++
	}
}
func (s *mmc3FakeSink) ClearIRQSource(src string) {
	if src == mmc3IRQSource {
		s.clears++
	}
}

// IRQ counter decrements on A12 rising edges + fires when reaching
// 0 with IRQ enabled.
func TestMMC3_IRQFiresOnUnderflow(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 4, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	sink := &mmc3FakeSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xC000, 2) // latch = 2
	c.CPUWrite(0xC001, 0) // schedule reload
	c.CPUWrite(0xE001, 0) // enable IRQ
	// First A12 rising: reload (counter = 2).
	c.PPURead(0x0000)
	c.PPURead(0x1000) // first rising → reload
	if sink.asserts != 0 {
		t.Errorf("IRQ fired on reload; want only on underflow")
	}
	// Counter is now 2. Two more rising edges should decrement to 0
	// + fire IRQ. Each rising needs a low between.
	c.PPURead(0x0000)
	c.PPURead(0x1000) // counter 2 → 1
	c.PPURead(0x0000)
	c.PPURead(0x1000) // counter 1 → 0 + fire
	if sink.asserts == 0 {
		t.Errorf("IRQ never fired on underflow")
	}
}

// $E000 disables + clears pending IRQ.
func TestMMC3_E000DisablesAndAcks(t *testing.T) {
	c, err := NewMMC3(fillMMC3Rom(t, 4, 8))
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	sink := &mmc3FakeSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xC000, 1)
	c.CPUWrite(0xC001, 0)
	c.CPUWrite(0xE001, 0)
	c.PPURead(0x0000)
	c.PPURead(0x1000) // reload
	c.PPURead(0x0000)
	c.PPURead(0x1000) // → 0 + fire
	if !c.irqPending {
		t.Fatalf("pre: expected IRQ pending")
	}
	c.CPUWrite(0xE000, 0)
	if c.irqPending {
		t.Errorf("$E000 didn't clear irqPending")
	}
	if sink.clears == 0 {
		t.Errorf("$E000 didn't call ClearIRQSource")
	}
}

// cart.Open dispatches mapper=4 to MMC3.
func TestCartOpen_DispatchesMMC3(t *testing.T) {
	rom := fillMMC3Rom(t, 2, 4)
	c, err := Open(rom)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := c.(*MMC3); !ok {
		t.Errorf("Open returned %T; want *MMC3", c)
	}
}
