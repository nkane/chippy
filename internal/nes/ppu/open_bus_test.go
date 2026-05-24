package ppu

import "testing"

// Reads from write-only registers ($2000 / $2001 / $2003 / $2005 /
// $2006) return whatever last crossed the PPU bus (the "open-bus
// latch"). Real silicon's DRAM-cell decay isn't modelled — the
// latch holds indefinitely.
func TestOpenBus_WriteOnlyRegistersReturnLatch(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// Any write seeds the latch.
	p.Write(0x2000, 0xAB)
	for _, addr := range []uint16{0x2000, 0x2001, 0x2003, 0x2005, 0x2006} {
		if got := p.Read(addr); got != 0xAB {
			t.Errorf("Read $%04X = $%02X; want $AB (open-bus)", addr, got)
		}
	}
}

// $2002 reads OR live status bits 5-7 with open-bus bits 0-4.
func TestOpenBus_Status_LowBitsFromLatch(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2000, 0xFF) // latch = $FF
	p.status = 0x80       // vblank set
	got := p.Read(0x2002)
	// Bits 7+5 from status, low 5 from latch (which was $FF →
	// low 5 = $1F).
	want := byte(0x80 | 0x1F)
	if got != want {
		t.Errorf("$2002 read = $%02X; want $%02X", got, want)
	}
}

// Palette reads place only 6 bits on the bus; upper 2 come from the
// latch.
func TestOpenBus_PaletteReadKeepsLatchUpperBits(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2000, 0xC0) // latch upper 2 bits = 11
	// Seed palette[0] = $05 via $2006 + $2007 path.
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x05)
	// Re-aim VRAM cursor at $3F00.
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	got := p.Read(0x2007)
	// Palette = $05 (6 bits) + open-bus upper 2 bits ($C0).
	// After the last $2006 second-write the latch was $00 (the
	// addr-low byte); upper 2 bits = 00. So the visible result is
	// just the palette value.
	want := byte(0x05)
	if got != want {
		t.Errorf("palette read = $%02X; want $%02X", got, want)
	}
}

// A $2004 read replaces the entire latch with the OAM byte.
func TestOpenBus_OAMReadUpdatesLatch(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2000, 0x00)
	p.oam[0] = 0x77
	if got := p.Read(0x2004); got != 0x77 {
		t.Errorf("$2004 = $%02X; want $77", got)
	}
	// Next read of a write-only register should see $77.
	if got := p.Read(0x2000); got != 0x77 {
		t.Errorf("post-OAM open-bus = $%02X; want $77", got)
	}
}
