package ppu

import "testing"

// loadCheckerTile fills the CHR-ROM tile at `tileIdx` (8×8) with the
// "low plane all 1s, high plane all 0s" pattern — every pixel value
// = 1 within the tile, the simplest "this pixel is opaque" mask.
func loadCheckerTile(cart *fakeCart, tileIdx int) {
	off := tileIdx * 16
	for r := range 8 {
		cart.chr[off+r] = 0xFF // low plane
		cart.chr[off+r+8] = 0x00
	}
}

// loadSpriteTile fills the CHR-ROM tile in the sprite pattern table
// at $1000-$1FFF (tile index space) so it doesn't collide with BG.
func loadSpriteTile(cart *fakeCart, tileIdx int) {
	off := 0x1000 + tileIdx*16
	for r := range 8 {
		cart.chr[off+r] = 0xFF
		cart.chr[off+r+8] = 0x00
	}
}

// clearOAM parks every sprite at Y=$FF (off-screen). Real ROMs do
// this during init; tests need it because uninitialized OAM
// otherwise stacks 64 phantom sprites at scanline 1 and corrupts the
// overflow / sprite-0 hit logic.
func clearOAM(p *PPU) {
	for i := range p.oam {
		if i%4 == 0 {
			p.oam[i] = 0xFF
		} else {
			p.oam[i] = 0
		}
	}
}

// setupPPUWithSprites builds a PPU + enables BG show + sprite show +
// configures the sprite pattern table at $1000 via PPUCTRL bit 3.
func setupPPUWithSprites(t *testing.T, cart *fakeCart) *PPU {
	t.Helper()
	p := New(cart, nil)
	clearOAM(p)
	// PPUMASK: BG show (bit 3) + sprite show (bit 4) = $18.
	// PPUCTRL: sprite pattern $1000 (bit 3).
	p.Write(0x2000, 0x08)
	p.Write(0x2001, 0x18)
	// Palette: universal $0F (black), BG[1] $30 (white), sprite[1] $16 (red).
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x0F) // $3F00 universal
	p.Write(0x2007, 0x30) // $3F01 BG[1]
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x11)
	p.Write(0x2007, 0x16) // $3F11 sprite[1]
	return p
}

// Single sprite, BG cleared (default tile $00 with empty pattern).
// Sprite at (X=64, Y=64), 8×8, tile index 1. After render, the 8×8
// box should carry the sprite's red color; the rest stays
// universal-bg.
func TestRenderSprites_SingleSpriteRenders(t *testing.T) {
	cart := &fakeCart{}
	loadSpriteTile(cart, 1)
	p := setupPPUWithSprites(t, cart)

	// Sprite 0 at (64, 64) tile 1, attr 0, x 64.
	p.Write(0x2003, 0)    // OAMADDR=0
	p.Write(0x2004, 63)   // Y (visible row = 64)
	p.Write(0x2004, 1)    // tile
	p.Write(0x2004, 0x00) // attr
	p.Write(0x2004, 64)   // X

	p.renderFrame()
	p.renderSprites()

	wantR, wantG, wantB := paletteRGB(0x16)
	off := (64*ScreenWidth + 64) * 4
	if p.frame[off+0] != wantR || p.frame[off+1] != wantG || p.frame[off+2] != wantB {
		t.Errorf("sprite pixel (64,64) = (%02X,%02X,%02X); want (%02X,%02X,%02X)",
			p.frame[off+0], p.frame[off+1], p.frame[off+2], wantR, wantG, wantB)
	}
	// A pixel far from the sprite should still be universal-bg black.
	bgR, bgG, bgB := paletteRGB(0x0F)
	off = (10*ScreenWidth + 10) * 4
	if p.frame[off+0] != bgR || p.frame[off+1] != bgG || p.frame[off+2] != bgB {
		t.Errorf("non-sprite pixel = (%02X,%02X,%02X); want bg (%02X,%02X,%02X)",
			p.frame[off+0], p.frame[off+1], p.frame[off+2], bgR, bgG, bgB)
	}
}

// Sprite 0 over an opaque BG pixel → status bit 6 set.
func TestRenderSprites_Sprite0HitFires(t *testing.T) {
	cart := &fakeCart{}
	// BG tile 0: all-opaque so every BG pixel = palette[1].
	loadCheckerTile(cart, 0)
	loadSpriteTile(cart, 1)
	p := setupPPUWithSprites(t, cart)

	p.Write(0x2003, 0)
	p.Write(0x2004, 63) // Y → visible row 64
	p.Write(0x2004, 1)  // tile (in sprite table)
	p.Write(0x2004, 0)  // attr (front, no flip)
	p.Write(0x2004, 64) // X

	p.renderFrame()
	if p.status&0x40 != 0 {
		t.Fatalf("sprite-0 hit set pre-renderSprites; want clean")
	}
	p.renderSprites()
	if p.status&0x40 == 0 {
		t.Errorf("sprite-0 hit should be set after opaque-over-opaque composite; status = $%02X", p.status)
	}
}

// Sprite 0 over a transparent BG region → status bit 6 stays clear.
func TestRenderSprites_Sprite0HitDoesNotFireOverTransparentBG(t *testing.T) {
	cart := &fakeCart{}
	loadSpriteTile(cart, 1)
	p := setupPPUWithSprites(t, cart)

	p.Write(0x2003, 0)
	p.Write(0x2004, 63)
	p.Write(0x2004, 1)
	p.Write(0x2004, 0)
	p.Write(0x2004, 64)

	p.renderFrame()
	p.renderSprites()
	if p.status&0x40 != 0 {
		t.Errorf("sprite-0 hit fired over transparent BG; status = $%02X", p.status)
	}
}

// Nine sprites on the same scanline → overflow flag set.
func TestRenderSprites_OverflowFiresWith9SpritesPerScanline(t *testing.T) {
	cart := &fakeCart{}
	loadSpriteTile(cart, 1)
	p := setupPPUWithSprites(t, cart)

	// 9 sprites all at Y=63 (visible row 64), different X — all
	// intersect scanline 64.
	for i := range 9 {
		p.Write(0x2003, byte(i*4))
		p.Write(0x2004, 63)
		p.Write(0x2004, 1)
		p.Write(0x2004, 0)
		p.Write(0x2004, byte(i*16)) // X stride
	}
	p.renderFrame()
	p.renderSprites()
	if p.status&0x20 == 0 {
		t.Errorf("overflow not set with 9 sprites on one scanline; status = $%02X", p.status)
	}
}

// Eight sprites on a scanline = OK (right at the limit). Overflow
// stays clear.
func TestRenderSprites_OverflowDoesNotFireAtEight(t *testing.T) {
	cart := &fakeCart{}
	loadSpriteTile(cart, 1)
	p := setupPPUWithSprites(t, cart)

	for i := range 8 {
		p.Write(0x2003, byte(i*4))
		p.Write(0x2004, 63)
		p.Write(0x2004, 1)
		p.Write(0x2004, 0)
		p.Write(0x2004, byte(i*16))
	}
	p.renderFrame()
	p.renderSprites()
	if p.status&0x20 != 0 {
		t.Errorf("overflow set with 8 sprites; want clear, status = $%02X", p.status)
	}
}

// Priority-behind-BG (attr bit 5) + opaque BG → sprite pixel hidden.
func TestRenderSprites_PriorityBehindBGHidesSprite(t *testing.T) {
	cart := &fakeCart{}
	loadCheckerTile(cart, 0) // BG tile $00 all-opaque
	loadSpriteTile(cart, 1)
	p := setupPPUWithSprites(t, cart)

	p.Write(0x2003, 0)
	p.Write(0x2004, 63)
	p.Write(0x2004, 1)
	p.Write(0x2004, 0x20) // attr: priority behind
	p.Write(0x2004, 64)

	p.renderFrame()
	p.renderSprites()

	// The pixel should be BG[1] (white-ish $30), NOT sprite[1] ($16).
	bgR, bgG, bgB := paletteRGB(0x30)
	off := (64*ScreenWidth + 64) * 4
	if p.frame[off+0] != bgR {
		t.Errorf("priority-behind sprite leaked through opaque BG: got (%02X,%02X,%02X); want BG (%02X,%02X,%02X)",
			p.frame[off+0], p.frame[off+1], p.frame[off+2], bgR, bgG, bgB)
	}
}

// 8×16 sprite mode: tile index bit 0 selects pattern table, tile
// stack covers 16 rows. Place a sprite and verify a pixel at the
// bottom half (row 12) renders as sprite color.
func TestRenderSprites_EightBySixteenRenders(t *testing.T) {
	cart := &fakeCart{}
	// In 8×16 mode: tile_idx=$02 → top tile $02 (pattern table $0000
	// since bit 0 = 0), bottom tile $03. Fill both halves.
	for r := range 8 {
		cart.chr[2*16+r] = 0xFF
		cart.chr[3*16+r] = 0xFF
	}
	p := New(cart, nil)
	clearOAM(p)
	// PPUCTRL: sprite size 8×16 (bit 5), pattern bit 3 ignored in 8×16 mode.
	p.Write(0x2000, 0x20)
	p.Write(0x2001, 0x18)
	// Palette
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x11)
	p.Write(0x2007, 0x16)

	p.Write(0x2003, 0)
	p.Write(0x2004, 63)   // Y → top row 64
	p.Write(0x2004, 0x02) // tile (top half)
	p.Write(0x2004, 0)
	p.Write(0x2004, 64)

	p.renderFrame()
	p.renderSprites()
	wantR, _, _ := paletteRGB(0x16)
	// Row 64+12 = inside the bottom half of an 8×16 sprite.
	off := ((64+12)*ScreenWidth + 64) * 4
	if p.frame[off+0] != wantR {
		t.Errorf("8x16 bottom-half pixel = $%02X; want sprite[1] red", p.frame[off+0])
	}
}

// Two sprites stacked at the same pixel: lower OAM index wins
// (drawn first; later sprite's same-pixel write is skipped).
func TestRenderSprites_LowerOAMIndexWinsPriority(t *testing.T) {
	cart := &fakeCart{}
	loadSpriteTile(cart, 1)
	loadSpriteTile(cart, 2)
	p := New(cart, nil)
	clearOAM(p)
	p.Write(0x2000, 0x08) // sprite pattern $1000
	p.Write(0x2001, 0x18)
	// sprite[1] = red ($16); palette select 1 (so $3F15)
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x15)
	p.Write(0x2007, 0x16)
	// sprite-palette-2[1] = blue ($12); palette select 2 (so $3F19)
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x19)
	p.Write(0x2007, 0x12)

	// Sprite 0: tile 1, palette 1 (red), at (64, 64).
	p.Write(0x2003, 0)
	p.Write(0x2004, 63)
	p.Write(0x2004, 1)
	p.Write(0x2004, 0x01) // palette select 1
	p.Write(0x2004, 64)
	// Sprite 1: tile 2, palette 2 (blue), same position.
	p.Write(0x2004, 63)
	p.Write(0x2004, 2)
	p.Write(0x2004, 0x02)
	p.Write(0x2004, 64)

	p.renderFrame()
	p.renderSprites()
	wantR, _, _ := paletteRGB(0x16) // red wins (sprite 0)
	off := (64*ScreenWidth + 64) * 4
	if p.frame[off+0] != wantR {
		t.Errorf("lower-OAM sprite should win: got R=$%02X want $%02X", p.frame[off+0], wantR)
	}
}

// PPUMASK bit 4 off → sprite layer suppressed entirely, even
// sprite-0 hit / overflow don't fire.
func TestRenderSprites_SpriteShowDisabledSuppressesEverything(t *testing.T) {
	cart := &fakeCart{}
	loadCheckerTile(cart, 0)
	loadSpriteTile(cart, 1)
	p := New(cart, nil)
	clearOAM(p)
	p.Write(0x2000, 0x08)
	p.Write(0x2001, 0x08) // BG show only — sprites off
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x0F)
	p.Write(0x2007, 0x30)
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x11)
	p.Write(0x2007, 0x16)

	// Nine sprites — would normally trigger overflow.
	for i := range 9 {
		p.Write(0x2003, byte(i*4))
		p.Write(0x2004, 63)
		p.Write(0x2004, 1)
		p.Write(0x2004, 0)
		p.Write(0x2004, byte(i*16))
	}
	p.renderFrame()
	p.renderSprites()
	if p.status&0x20 != 0 {
		t.Errorf("overflow set despite sprite-show off; status = $%02X", p.status)
	}
	if p.status&0x40 != 0 {
		t.Errorf("sprite-0 hit set despite sprite-show off; status = $%02X", p.status)
	}
}
