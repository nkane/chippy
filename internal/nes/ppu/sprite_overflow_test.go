package ppu

import "testing"

// The 2C02 sprite-overflow evaluator has a documented bug (#283):
// after 8 in-range sprites are found, it keeps scanning for a 9th
// but increments the byte index m alongside the sprite index n on a
// miss, reading tile / attribute / X bytes as if they were Y. This
// produces false-positive overflow flags games rely on.
//
// Construct exactly 8 truly-in-range sprites on scanline 64, then a
// 9th sprite that is OUT of range by its real Y but whose TILE byte
// (read via the drifted m) looks like an in-range Y. The buggy
// evaluator fires overflow; a simple-correct counter would not.
func TestSpriteOverflow_SiliconFalsePositive(t *testing.T) {
	p := New(&fakeCart{}, nil)

	// Sprites 0..7 in range on scanline 64 (Y byte 63 → drawn row 64).
	for i := 0; i < 8; i++ {
		p.oam[i*4+0] = 63
		p.oam[i*4+1] = 1
		p.oam[i*4+2] = 0
		p.oam[i*4+3] = byte(i * 16)
	}
	// Sprite 8: out of range by real Y.
	p.oam[8*4+0] = 200
	p.oam[8*4+1] = 0
	p.oam[8*4+2] = 0
	p.oam[8*4+3] = 0
	// Sprite 9: real Y out of range, but TILE byte = 63 so the
	// drifted m=1 read (OAM[4*9+1]) looks in-range for scanline 64.
	p.oam[9*4+0] = 200 // real Y, out of range
	p.oam[9*4+1] = 63  // tile byte — the bug reads THIS as Y
	p.oam[9*4+2] = 0
	p.oam[9*4+3] = 0
	// Everything else far off-screen.
	for i := 10; i < 64; i++ {
		p.oam[i*4+0] = 0xFF
		p.oam[i*4+1] = 0xFF
		p.oam[i*4+2] = 0xFF
		p.oam[i*4+3] = 0xFF
	}

	p.status = 0
	p.evaluateSpriteOverflow(64, 8)
	if p.status&0x20 == 0 {
		t.Errorf("silicon bug not reproduced: overflow clear with the crafted m-drift case")
	}
}

// Exactly 8 in-range sprites + all others genuinely off-screen
// ($FF bytes) → no overflow. The drifted reads only ever see $FF
// (Y=255 → never in range), so the bug doesn't spuriously fire.
func TestSpriteOverflow_EightInRangeNoFalsePositive(t *testing.T) {
	p := New(&fakeCart{}, nil)
	for i := 0; i < 8; i++ {
		p.oam[i*4+0] = 63
		p.oam[i*4+1] = 1
		p.oam[i*4+2] = 0
		p.oam[i*4+3] = byte(i * 16)
	}
	for i := 8; i < 64; i++ {
		p.oam[i*4+0] = 0xFF
		p.oam[i*4+1] = 0xFF
		p.oam[i*4+2] = 0xFF
		p.oam[i*4+3] = 0xFF
	}
	p.status = 0
	p.evaluateSpriteOverflow(64, 8)
	if p.status&0x20 != 0 {
		t.Errorf("false overflow with 8 in-range + off-screen rest; status=$%02X", p.status)
	}
}

// Nine genuinely-in-range sprites → overflow (true positive). The
// evaluator finds the 9th with m still 0 before any drift.
func TestSpriteOverflow_NineInRangeTruePositive(t *testing.T) {
	p := New(&fakeCart{}, nil)
	for i := 0; i < 9; i++ {
		p.oam[i*4+0] = 63
		p.oam[i*4+1] = 1
		p.oam[i*4+2] = 0
		p.oam[i*4+3] = byte(i * 8)
	}
	for i := 9; i < 64; i++ {
		p.oam[i*4+0] = 0xFF
	}
	p.status = 0
	p.evaluateSpriteOverflow(64, 8)
	if p.status&0x20 == 0 {
		t.Errorf("overflow not set with 9 genuinely-in-range sprites")
	}
}
