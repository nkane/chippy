package ppu

import "testing"

// stepToDot advances the PPU one dot at a time until it sits exactly on
// (scanline, dot). Fails the test if it doesn't arrive within a frame.
func stepToDot(t *testing.T, p *PPU, scanline, dot int) {
	t.Helper()
	for range p.timing.ScanlinesPerFrame * p.timing.DotsPerScanline {
		p.stepDot()
		if p.scanline == scanline && p.dot == dot {
			return
		}
	}
	t.Fatalf("never reached scanline %d dot %d (stuck at %d,%d)", scanline, dot, p.scanline, p.dot)
}

// The vblank flag sets at scanline 241 dot 1 when no read contends for
// that dot.
func TestVblRace_FlagSetsAtSetDot(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 1)
	if p.status&0x80 == 0 {
		t.Fatalf("vblank flag not set at (241,1); status=$%02X", p.status)
	}
}

// 2C02 race: a $2002 read landing on the exact set dot reads bit 7 as 0
// (and clears the flag).
func TestVblRace_ReadOnSetDotReadsZero(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 1)
	if got := p.Read(0x2002); got&0x80 != 0 {
		t.Errorf("read on set dot = $%02X; want bit 7 clear (race)", got)
	}
}

// One dot after the set, the read sees the flag set (and clears it).
func TestVblRace_ReadAfterSetDotReadsSet(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 2)
	if got := p.Read(0x2002); got&0x80 == 0 {
		t.Errorf("read one dot after set = $%02X; want bit 7 set", got)
	}
}

// A $2002 read one dot before the set does NOT suppress: the flag still
// sets, so a later read sees it (Blargg 02-vbl_set_time T+3 = "- V").
func TestVblRace_ReadBeforeSetDoesNotSuppress(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 0) // dot immediately before the set
	if got := p.Read(0x2002); got&0x80 != 0 {
		t.Fatalf("pre-set read = $%02X; want bit 7 clear (not set yet)", got)
	}
	p.stepDot() // set dot (241,1)
	if p.status&0x80 == 0 {
		t.Errorf("flag should still set after a pre-set read; status=$%02X", p.status)
	}
}

// 2C02 clear race: a $2002 read on the exact auto-clear dot (pre-render,
// dot 1) still reads the flag as set — the read wins, seeing the
// pre-clear value (Blargg 03-vbl_clear_time T+5 = "V").
func TestVblRace_ReadOnClearDotReadsSet(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 1) // raise the flag
	if p.status&0x80 == 0 {
		t.Fatal("flag not raised at (241,1)")
	}
	stepToDot(t, p, p.timing.PreRenderScanline, 1) // auto-clear dot
	if got := p.Read(0x2002); got&0x80 == 0 {
		t.Errorf("read on clear dot = $%02X; want bit 7 set (pre-clear value)", got)
	}
}
