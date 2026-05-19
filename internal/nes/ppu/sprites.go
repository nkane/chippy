package ppu

// Sprite rendering pipeline. v0.2 is per-frame, not per-scanline:
// after renderFrame paints the BG layer (and populates bgOpaque),
// renderSprites walks OAM once and composites visible sprites on top.
// Sprite-0 hit and sprite-overflow flags are set during the same pass.
//
// Out of scope here (v0.x stretch / cycle-accurate work):
//   - Cycle-accurate sprite-0 hit timing (the *exact* dot the flag
//     sets matters for a handful of games that race their split-
//     screen tricks against $2002 polls).
//   - The sprite-overflow "bug" — real silicon mis-evaluates the OAM
//     pointer once more than 8 sprites are in range, producing
//     hardware-specific false positives. v0.2 implements the simple
//     correct version.
//   - $2007-during-rendering and other dot-exact register quirks.
//
// OAM byte layout (4 bytes per sprite, 64 sprites total):
//
//	byte 0: Y position (sprite is drawn at Y+1 — the 2C02 latches Y
//	        on the prior scanline, so the visible row is one below)
//	byte 1: tile index (in 8×16 mode bit 0 selects pattern table,
//	        the top half = tile_id & ~1, bottom = tile_id | 1)
//	byte 2: attributes
//	          bits 0-1 : palette select (sprite palette $3F10-$3F1F)
//	          bit 5    : priority (0 = sprite in front of BG, 1 = behind)
//	          bit 6    : horizontal flip
//	          bit 7    : vertical flip
//	byte 3: X position

// renderSprites composites the sprite layer over the BG. Mutates
// p.frame; reads p.bgOpaque; updates p.status (sprite-0 hit + sprite
// overflow flags).
func (p *PPU) renderSprites() {
	// PPUMASK bit 4 = show sprites. When off, the sprite layer is
	// suppressed entirely. Overflow / sprite-0 hit don't fire because
	// nothing renders.
	if p.mask&0x10 == 0 {
		return
	}

	spriteH := 8
	if p.ctrl&0x20 != 0 {
		spriteH = 16
	}

	// First pass: per-scanline sprite count to drive overflow.
	// Real silicon sets overflow only when secondary OAM fills past
	// 8 entries on a scanline; we approximate by counting how many
	// sprites' Y-ranges intersect each visible scanline.
	overflow := false
	for y := range ScreenHeight {
		count := 0
		for i := range 64 {
			topY := int(p.oam[i*4+0]) + 1
			if y < topY || y >= topY+spriteH {
				continue
			}
			count++
			if count > 8 {
				overflow = true
				break
			}
		}
		if overflow {
			break
		}
	}
	if overflow {
		p.status |= 0x20
	}

	// Second pass: composite. Track per-pixel sprite-drawn so lower
	// OAM indices (drawn first → higher priority) aren't overwritten
	// by later sprites.
	var spritePainted [ScreenWidth * ScreenHeight]bool

	// Sprite-0 hit gating: requires both BG show + sprite show. The
	// real silicon has a stricter set of conditions (x != 255, not in
	// the left-8-px clipping windows, etc.); v0.2 honors the two
	// PPUMASK gates and skips the corner cases.
	canHitSprite0 := p.mask&0x08 != 0 && p.mask&0x10 != 0

	// Pattern-table base for sprites. In 8×8 mode PPUCTRL bit 3
	// selects the base. In 8×16 mode the tile index's bit 0 selects;
	// PPUCTRL bit 3 is ignored.
	sprPatternBase := uint16(0)
	if p.ctrl&0x08 != 0 && p.ctrl&0x20 == 0 {
		sprPatternBase = 0x1000
	}

	for i := range 64 {
		spriteY := int(p.oam[i*4+0]) + 1
		tileIdx := p.oam[i*4+1]
		attr := p.oam[i*4+2]
		spriteX := int(p.oam[i*4+3])
		paletteSel := attr & 0x03
		priorityBehind := attr&0x20 != 0
		hflip := attr&0x40 != 0
		vflip := attr&0x80 != 0

		// Bail if entirely off-screen vertically.
		if spriteY >= ScreenHeight {
			continue
		}

		for row := 0; row < spriteH; row++ {
			py := spriteY + row
			if py < 0 || py >= ScreenHeight {
				continue
			}
			// Fine-Y within the 8×8 (or 8×16) sprite, accounting for
			// vertical flip.
			fineY := row
			if vflip {
				fineY = spriteH - 1 - row
			}

			// Pick the actual 8×8 tile + fine-Y-within-tile. 8×16
			// sprites stack two tiles vertically and select the
			// pattern table via the tile-index LSB.
			var tileAddr uint16
			if spriteH == 16 {
				base := uint16(0)
				if tileIdx&1 != 0 {
					base = 0x1000
				}
				tileNum := uint16(tileIdx & 0xFE)
				if fineY >= 8 {
					tileNum |= 1
				}
				tileAddr = base + tileNum*16 + uint16(fineY&7)
			} else {
				tileAddr = sprPatternBase + uint16(tileIdx)*16 + uint16(fineY)
			}

			low := p.busRead(tileAddr)
			high := p.busRead(tileAddr + 8)

			for col := range 8 {
				px := spriteX + col
				if px < 0 || px >= ScreenWidth {
					continue
				}
				bitCol := col
				if !hflip {
					bitCol = 7 - col
				}
				bit := uint(bitCol)
				lo := (low >> bit) & 1
				hi := (high >> bit) & 1
				val := (hi << 1) | lo
				if val == 0 {
					continue // transparent
				}

				pxIdx := py*ScreenWidth + px

				// Sprite-0 hit: when *any* opaque pixel of sprite 0
				// would composite over an opaque BG pixel, latch the
				// flag (and keep it latched until end-of-vblank).
				// The hit fires even when the priority bit hides the
				// sprite behind BG — real silicon checks the overlap,
				// not the visible result.
				if i == 0 && canHitSprite0 && p.bgOpaque[pxIdx] {
					p.status |= 0x40
				}

				// Skip drawing where an earlier sprite already painted
				// (lower OAM index wins on real silicon).
				if spritePainted[pxIdx] {
					continue
				}
				spritePainted[pxIdx] = true

				// Priority-behind-BG: opaque BG pixel hides this
				// sprite pixel.
				if priorityBehind && p.bgOpaque[pxIdx] {
					continue
				}

				// Sprite palette lives at $3F10-$3F1F.
				colorIdx := p.palette[0x10|(paletteSel<<2)|val]
				r, g, b := paletteRGB(colorIdx)
				off := pxIdx * 4
				p.frame[off+0] = r
				p.frame[off+1] = g
				p.frame[off+2] = b
				p.frame[off+3] = 0xFF
			}
		}
	}
}
