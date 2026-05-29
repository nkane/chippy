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
// the preceding dot 0.
func TestVblRace_FlagSetsAtSetDot(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 1)
	if p.status&0x80 == 0 {
		t.Fatalf("vblank flag not set at (241,1); status=$%02X", p.status)
	}
}

// 2C02 race per Mesen2 model: a $2002 read on the PPU clock immediately
// BEFORE vblank-set (scanline 241, dot 0) reads bit 7 clear AND latches
// preventVblFlag — the flag never sets for this frame, and the next
// dot's vblank-set check is skipped entirely.
func TestVblRace_ReadOnDotBeforeSetSuppressesFrame(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 0)
	if got := p.Read(0x2002); got&0x80 != 0 {
		t.Errorf("read on (241,0) = $%02X; want bit 7 clear", got)
	}
	if !p.preventVblFlag {
		t.Errorf("read on (241,0) didn't latch preventVblFlag")
	}
	p.stepDot() // advance to (241,1) — would-be set dot
	if p.status&0x80 != 0 {
		t.Errorf("vblank flag set despite preventVblFlag; status=$%02X", p.status)
	}
	if p.preventVblFlag {
		t.Errorf("preventVblFlag should clear after the suppressed set check")
	}
}

// A read landing exactly on the set dot (241,1) reads the flag set —
// the dot-0 race didn't fire, the set fired, the read sees it.
func TestVblRace_ReadOnSetDotReadsSet(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 1)
	if got := p.Read(0x2002); got&0x80 == 0 {
		t.Errorf("read on (241,1) = $%02X; want bit 7 set", got)
	}
}

// 2C02 vblank-CLEAR behavior: a $2002 read AFTER the auto-clear dot
// reads the flag as clear. The "read on clear dot still sees set"
// race used to be explicit in PPU code, but with the master-clock
// model (#372 redesign) it emerges from CPU read timing — PPU.Run is
// called with deadline = masterClock-1 BEFORE the bus access, leaving
// PPU at "1 PPU clock before clear" so the read observes the pre-
// clear flag. End-to-end coverage lives in Blargg 03-vbl_clear_time;
// the dot-level unit test that pinned the old race model was deleted.
func TestVblRace_ReadAfterClearReadsClear(t *testing.T) {
	p := New(&fakeCart{}, &fakeNMI{})
	stepToDot(t, p, 241, 1) // raise the flag
	if p.status&0x80 == 0 {
		t.Fatal("flag not raised at (241,1)")
	}
	stepToDot(t, p, p.timing.PreRenderScanline, 2) // 1 dot past auto-clear
	if got := p.Read(0x2002); got&0x80 != 0 {
		t.Errorf("read after clear dot = $%02X; want bit 7 clear", got)
	}
}
