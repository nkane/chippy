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

// compositeScanlineSprites paints sprite pixels for one scanline
// over the BG layer that renderScanlineEnabled just wrote. Walks
// OAM 0..63 forward (lower index wins on overlap, matching real
// silicon priority). Updates sprite-overflow flag based on this
// scanline's sprite count. Designed to fire right after the BG
// scanline render so the framebuffer is "complete" for that row
// before Ebiten's Draw can sample it.
// evaluateSpriteOverflow reproduces the 2C02's buggy sprite-overflow
// flag (#283). Real silicon evaluates OAM into secondary OAM during
// each visible scanline; once 8 sprites are found it keeps scanning
// for a 9th but, on a NOT-in-range result, increments BOTH the sprite
// index n AND the byte index m (instead of resetting m to 0). The
// floating m makes it read tile-index / attribute / X bytes as if
// they were Y coordinates, producing the hardware-specific false
// positives + false negatives that games like Battletoads lean on.
//
// y is the visible scanline; spriteY in the OAM is stored as
// (drawn_y - 1), so the in-range test compares against oam[base]+1
// matching the compositor's spriteY convention.
func (p *PPU) evaluateSpriteOverflow(y, spriteH int) {
	n := 0 // sprite index 0..63
	m := 0 // byte index within a sprite; should stay 0, the bug drifts it
	count := 0
	for n < 64 {
		yByte := int(p.oam[(4*n+m)&0xFF])
		spriteY := yByte + 1
		inRange := y >= spriteY && y < spriteY+spriteH
		if count < 8 {
			if inRange {
				count++
			}
			n++ // normal scan: advance to next sprite, m stays 0
			continue
		}
		// count == 8 — scanning for the 9th sprite.
		if inRange {
			p.status |= 0x20 // overflow latched
			return
		}
		// Hardware bug: m drifts alongside n on a miss.
		m = (m + 1) & 3
		n++
	}
}

func (p *PPU) compositeScanlineSprites(y int) {
	if p.mask&0x10 == 0 {
		return
	}
	spriteH := 8
	if p.ctrl&0x20 != 0 {
		spriteH = 16
	}
	sprPatternBase := uint16(0)
	if p.ctrl&0x08 != 0 && p.ctrl&0x20 == 0 {
		sprPatternBase = 0x1000
	}
	// Sprite-overflow flag uses the silicon's buggy evaluator
	// (#283) — not a simple count. See evaluateSpriteOverflow.
	p.evaluateSpriteOverflow(y, spriteH)
	// Track which screen-x columns of this scanline a sprite has
	// already painted (lower OAM idx wins on overlap).
	var painted [ScreenWidth]bool
	for i := 0; i < 64; i++ {
		spriteY := int(p.oam[i*4+0]) + 1
		if y < spriteY || y >= spriteY+spriteH {
			continue
		}
		tileIdx := p.oam[i*4+1]
		attr := p.oam[i*4+2]
		spriteX := int(p.oam[i*4+3])
		paletteSel := attr & 0x03
		priorityBehind := attr&0x20 != 0
		hflip := attr&0x40 != 0
		vflip := attr&0x80 != 0

		row := y - spriteY
		fineY := row
		if vflip {
			fineY = spriteH - 1 - row
		}
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
		for col := 0; col < 8; col++ {
			px := spriteX + col
			if px < 0 || px >= ScreenWidth {
				continue
			}
			bitCol := col
			if !hflip {
				bitCol = 7 - col
			}
			bit := uint(bitCol)
			val := ((high>>bit)&1)<<1 | ((low >> bit) & 1)
			if val == 0 {
				continue
			}
			pxIdx := y*ScreenWidth + px
			if painted[px] {
				continue // earlier sprite already won this pixel
			}
			painted[px] = true
			if priorityBehind && p.bgOpaque[pxIdx] {
				continue
			}
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

	// First pass: drive the sprite-overflow flag through the silicon's
	// buggy evaluator (#283), once per visible scanline. The
	// per-scanline composite path (compositeScanlineSprites) calls
	// the same evaluator; this legacy per-frame path mirrors it so
	// direct renderSprites() callers (tests) see identical flag
	// behaviour.
	for y := range ScreenHeight {
		p.evaluateSpriteOverflow(y, spriteH)
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
