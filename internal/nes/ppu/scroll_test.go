package ppu

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// twoTilePPU builds a PPU whose nametable 0 holds tile $00 in column
// 0 and tile $01 everywhere else, so a horizontal scroll shift is
// visually detectable as the column-0 tile sliding off-screen.
// Pattern table:
//
//	tile $00: low plane all 0s   → pixel value 0 everywhere
//	tile $01: low plane all 1s   → pixel value 1 everywhere
//
// With BG show + palette[0]=$0F (black) + palette[1]=$30 (white-ish),
// tile $00 reads black and tile $01 reads white.
func twoTilePPU(t *testing.T) *PPU {
	t.Helper()
	cart := &fakeCart{mir: nes.MirrorHorizontal}
	// tile $00: empty pattern (all transparent → universal bg)
	// tile $01: low plane all 1s
	for r := range 8 {
		cart.chr[0x10+r] = 0xFF // low plane of tile $01
		cart.chr[0x18+r] = 0x00 // high plane of tile $01
	}
	p := New(cart, nil)
	clearOAM(p)
	p.Write(0x2001, 0x08) // BG show
	// Palette
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x0F) // universal bg = black
	p.Write(0x2007, 0x30) // BG[1]
	// Fill nametable 0 with tile $01 everywhere, then make column 0
	// tile $00 (transparent).
	for row := range 30 {
		for col := range 32 {
			p.Write(0x2006, 0x20|byte(row>>4))
			p.Write(0x2006, byte(row<<4)|byte(col&0x0F))
			// The two $2006 writes form the address $20RC where
			// R = row 0..29, C = col 0..31. Pack via standard
			// nametable address math.
		}
	}
	// The above $2006 sequence is wrong (encoded wrong). Bypass:
	// poke vram directly.
	for row := range 30 {
		for col := range 32 {
			if col == 0 {
				p.vram[row*32+col] = 0x00
			} else {
				p.vram[row*32+col] = 0x01
			}
		}
	}
	return p
}

// Horizontal scroll: shift left by 8 px. The column-0 (tile $00,
// transparent) should move off-screen, so x=0 now reads tile $01
// (white).
func TestRenderScanline_HorizontalScrollShiftsLeft(t *testing.T) {
	p := twoTilePPU(t)

	// Pre-scroll: x=0 row=64 should be tile $00 → universal bg (black).
	snap := p.frameStartScroll
	p.renderScanline(64, snap)
	bgR, _, _ := paletteRGB(0x0F)
	whiteR, _, _ := paletteRGB(0x30)
	if p.frame[(64*ScreenWidth+0)*4+0] != bgR {
		t.Fatalf("pre-scroll x=0 should be tile $00 (black); got R=$%02X",
			p.frame[(64*ScreenWidth+0)*4+0])
	}

	// Apply scroll +8.
	snap.scrollX = 8
	p.renderScanline(64, snap)
	if p.frame[(64*ScreenWidth+0)*4+0] != whiteR {
		t.Errorf("post-scroll x=0 should now show tile $01 (white); got R=$%02X",
			p.frame[(64*ScreenWidth+0)*4+0])
	}
}

// Mid-frame scroll split: render with the frame-start scroll set to
// 0, then trigger a mid-frame event at scanline 32 that sets
// scrollX=8. Rows 0..31 should still show the original tile $00 at
// x=0; rows 32+ should show tile $01.
func TestRenderFrame_MidFrameSplit(t *testing.T) {
	p := twoTilePPU(t)
	// Frame-start scroll = (0, 0) on nametable 0.
	p.frameStartScroll = scrollSnapshot{scanline: 0, scrollX: 0, scrollY: 0, baseNametable: 0}
	// Mid-frame event: at scanline 32, scrollX flips to 8.
	p.scrollEvents = []scrollSnapshot{
		{scanline: 32, scrollX: 8, scrollY: 0, baseNametable: 0},
	}

	p.renderFrame()

	bgR, _, _ := paletteRGB(0x0F)
	whiteR, _, _ := paletteRGB(0x30)
	// Scanline 0 (pre-split): x=0 = tile $00 (black).
	if got := p.frame[(0*ScreenWidth+0)*4+0]; got != bgR {
		t.Errorf("scanline 0 x=0 = $%02X; want black (pre-split tile $00)", got)
	}
	// Scanline 31 (last row before split): still pre-split.
	if got := p.frame[(31*ScreenWidth+0)*4+0]; got != bgR {
		t.Errorf("scanline 31 x=0 = $%02X; want black (pre-split)", got)
	}
	// Scanline 32 (first row after split): scrollX=8 hides tile $00,
	// so x=0 now reads tile $01 (white).
	if got := p.frame[(32*ScreenWidth+0)*4+0]; got != whiteR {
		t.Errorf("scanline 32 x=0 = $%02X; want white (post-split tile $01)", got)
	}
	// Scanline 100 (well past split): still post-split.
	if got := p.frame[(100*ScreenWidth+0)*4+0]; got != whiteR {
		t.Errorf("scanline 100 x=0 = $%02X; want white", got)
	}
}

// Horizontal nametable wrap: scrollX=200 + base nametable 0. Tile
// $00 lives at column 0 in nametable 0. With scrollX=200, the right
// edge of the visible window crosses into nametable 1 — we don't
// preload nametable 1's tiles, so they read as $00 by default. The
// purpose of this test is to assert the wrap doesn't crash and the
// nametable index actually flips (vram[$0400] etc. would be touched
// by the busRead path — fakeCart's nametable mirroring keeps things
// consistent).
func TestRenderScanline_HorizontalNametableWrap(t *testing.T) {
	cart := &fakeCart{mir: nes.MirrorVertical}
	// Tile $05 in pattern table: all-opaque.
	for r := range 8 {
		cart.chr[0x50+r] = 0xFF
	}
	p := New(cart, nil)
	clearOAM(p)
	p.Write(0x2001, 0x08)
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x0F)
	p.Write(0x2007, 0x30)
	// Pre-load NT0 with tile $05 everywhere (so the visible window
	// shows white). NT1 stays at default tile $00 (black).
	for i := range p.vram[:0x400] {
		p.vram[i] = 0x05
	}
	// Scroll +200 → first ~56 px from NT0, remaining ~200 px from NT1.
	snap := scrollSnapshot{scrollX: 200, baseNametable: 0}
	p.renderScanline(50, snap)

	whiteR, _, _ := paletteRGB(0x30)
	bgR, _, _ := paletteRGB(0x0F)
	// x=0: still inside NT0 (effX = 200, well inside 0..255).
	if p.frame[(50*ScreenWidth+0)*4+0] != whiteR {
		t.Errorf("x=0 should be NT0 tile $05 (white)")
	}
	// x=100: effX = 300 → wraps to NT1 effX=44 → tile $00 (black).
	if p.frame[(50*ScreenWidth+100)*4+0] != bgR {
		t.Errorf("x=100 (post-wrap) should be NT1 tile $00 (black); got R=$%02X",
			p.frame[(50*ScreenWidth+100)*4+0])
	}
}

// stepDot frame-start snapshot: write scroll during simulated
// vblank, advance PPU through one frame; frameStartScroll should
// reflect the vblank write, not whatever was active at scanline 240.
func TestStepDot_SnapshotsScrollAtFrameStart(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// Game writes scroll values directly via $2005.
	p.Write(0x2005, 0x42) // scrollX
	p.Write(0x2005, 0x84) // scrollY
	// Advance one full frame so stepDot fires the scanline-rollback
	// transition that snaps frameStartScroll. 89342 dots = 1 frame.
	p.Tick(89342 / 3)
	if p.frameStartScroll.scrollX != 0x42 {
		t.Errorf("frameStartScroll.scrollX = $%02X; want $42", p.frameStartScroll.scrollX)
	}
	if p.frameStartScroll.scrollY != 0x84 {
		t.Errorf("frameStartScroll.scrollY = $%02X; want $84", p.frameStartScroll.scrollY)
	}
}

// recordScrollChange filters out writes during vblank (scanline >=
// 240): those land in frameStartScroll, not the per-frame event log.
func TestRecordScrollChange_IgnoresVblankWrites(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.scanline = 250 // vblank
	p.Write(0x2005, 0x10)
	p.Write(0x2005, 0x20) // both writes during vblank
	if len(p.scrollEvents) != 0 {
		t.Errorf("vblank writes leaked into scrollEvents: %d entries", len(p.scrollEvents))
	}
}

// recordScrollChange logs writes during visible scanlines.
func TestRecordScrollChange_CapturesVisibleSplit(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.scanline = 32 // mid-frame
	p.Write(0x2005, 0x08)
	p.Write(0x2005, 0x00) // second $2005 write also fires recordScroll
	if len(p.scrollEvents) < 2 {
		t.Errorf("mid-frame $2005 pair should produce events; got %d", len(p.scrollEvents))
	}
	for _, ev := range p.scrollEvents {
		if ev.scanline != 32 {
			t.Errorf("event scanline = %d; want 32", ev.scanline)
		}
	}
}
