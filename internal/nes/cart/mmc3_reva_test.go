package cart

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

func newMMC3ForRev(t *testing.T, sub uint8) *MMC3 {
	t.Helper()
	rom := &nes.ROM{
		Mapper:    4,
		SubMapper: sub,
		PRG:       make([]byte, 32*1024),
		CHR:       make([]byte, 8*1024),
		Mirroring: nes.MirrorHorizontal,
	}
	c, err := NewMMC3(rom)
	if err != nil {
		t.Fatalf("NewMMC3: %v", err)
	}
	return c
}

// pulseA12 emulates one A12 rising edge: addr toggles low → high.
func pulseA12(c *MMC3) {
	c.PPURead(0x0000) // A12 = 0
	c.PPURead(0x1000) // A12 = 1 (rising)
}

// RevB (default sub-mapper 0): explicit $C001 reload with latch=0 +
// IRQ enabled fires on the next A12 edge.
func TestMMC3_RevB_ReloadWithZeroLatchFires(t *testing.T) {
	c := newMMC3ForRev(t, 0)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xC000, 0) // latch = 0
	c.CPUWrite(0xC001, 0) // reload flag
	c.CPUWrite(0xE001, 0) // IRQ enable on
	pulseA12(c)
	if sink.asserts != 1 {
		t.Errorf("RevB explicit-reload-zero asserts = %d; want 1", sink.asserts)
	}
}

// RevA (sub-mapper 3): same setup, no IRQ. The explicit reload
// path silently loads + skips the post-reload IRQ check.
func TestMMC3_RevA_ReloadWithZeroLatchSilent(t *testing.T) {
	c := newMMC3ForRev(t, 3)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xC000, 0)
	c.CPUWrite(0xC001, 0)
	c.CPUWrite(0xE001, 0)
	pulseA12(c)
	if sink.asserts != 0 {
		t.Errorf("RevA explicit-reload-zero asserted: %d", sink.asserts)
	}
}

// Natural countdown path still fires on RevA — only the explicit-
// reload IRQ is suppressed.
func TestMMC3_RevA_NaturalCountdownStillFires(t *testing.T) {
	c := newMMC3ForRev(t, 3)
	sink := &fakeIRQSink{}
	c.SetIRQSink(sink)
	c.CPUWrite(0xC000, 1) // latch = 1
	c.CPUWrite(0xC001, 0) // reload
	c.CPUWrite(0xE001, 0) // enable
	// First A12 rising: reload path (silent for RevA), counter = 1.
	pulseA12(c)
	if sink.asserts != 0 {
		t.Errorf("RevA reload phase asserted")
	}
	// Second A12: counter 1 → 0, fires.
	pulseA12(c)
	if sink.asserts != 1 {
		t.Errorf("RevA natural countdown asserts = %d; want 1", sink.asserts)
	}
}
