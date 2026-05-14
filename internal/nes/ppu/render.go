package ppu

// renderFrame fills the 256 × 240 RGBA framebuffer with the background
// layer using the PPU's state at this moment (typically vblank entry).
//
// v0.1 is per-frame, not per-scanline: we don't model mid-frame scroll
// changes or split-screen tricks. Static title screens render correctly;
// scrolling / status-bar games will show artifacts (deferred to v0.2's
// per-scanline pipeline with the v/t/x/w latch model).
//
// Pipeline (matches the issue spec's "every 8 dots, fetch a tile"):
//
//  1. Walk 30 tile-rows × 32 tile-columns over the visible 240 × 256
//     pixel grid.
//  2. For each tile: fetch nametable byte (the pattern index), fetch
//     the attribute byte (the 2-bit palette select for this 2×2 tile
//     quadrant), fetch the two pattern-table planes (low + high) for
//     each of the tile's 8 rows.
//  3. Combine plane bits to a 2-bit pixel value (0-3); value 0 is the
//     universal background color ($3F00); 1-3 index into the selected
//     4-color sub-palette ($3F00 + (paletteSelect<<2) + value).
//  4. Map the 6-bit palette-RAM byte through the 64-color NES palette
//     to an RGB triple and write (R, G, B, 0xFF) into the framebuffer.
func (p *PPU) renderFrame() {
	if p.mask&0x08 == 0 {
		// PPUMASK bit 3 = "show background". When off, the screen is
		// the universal background color (clipped to the same NES
		// palette).
		r, g, b := paletteRGB(p.palette[0])
		for i := 0; i < len(p.frame); i += 4 {
			p.frame[i+0] = r
			p.frame[i+1] = g
			p.frame[i+2] = b
			p.frame[i+3] = 0xFF
		}
		return
	}

	// PPUCTRL bits 0-1: base nametable address. v0.1 uses this
	// directly since we don't apply scroll within a frame.
	nametableBase := uint16(0x2000) + uint16(p.ctrl&0x03)*0x0400
	// PPUCTRL bit 4: pattern-table base for background tiles.
	patternBase := uint16(0)
	if p.ctrl&0x10 != 0 {
		patternBase = 0x1000
	}

	for tileY := range 30 {
		for tileX := range 32 {
			ntAddr := nametableBase + uint16(tileY)*32 + uint16(tileX)
			tileIdx := p.busRead(ntAddr)

			// Attribute table lives at $23C0 / $27C0 / ... within the
			// same nametable bank. Each byte covers a 4 × 4 tile
			// region (32 × 32 pixels); within that, two bits per
			// 2 × 2 tile quadrant.
			attrAddr := nametableBase + 0x03C0 + uint16(tileY/4)*8 + uint16(tileX/4)
			attr := p.busRead(attrAddr)
			quadrantShift := uint(((tileY%4)/2)*4 + ((tileX%4)/2)*2)
			paletteSelect := (attr >> quadrantShift) & 0x03

			// Fetch 8 rows of the tile pattern.
			tileAddr := patternBase + uint16(tileIdx)*16
			for fineY := range 8 {
				low := p.busRead(tileAddr + uint16(fineY))
				high := p.busRead(tileAddr + uint16(fineY) + 8)
				py := tileY*8 + fineY
				rowBase := py * ScreenWidth * 4
				for fineX := range 8 {
					bit := uint(7 - fineX)
					lo := (low >> bit) & 1
					hi := (high >> bit) & 1
					val := (hi << 1) | lo

					var colorIdx byte
					if val == 0 {
						colorIdx = p.palette[0]
					} else {
						colorIdx = p.palette[(paletteSelect<<2)|val]
					}
					r, g, b := paletteRGB(colorIdx)

					px := tileX*8 + fineX
					off := rowBase + px*4
					p.frame[off+0] = r
					p.frame[off+1] = g
					p.frame[off+2] = b
					p.frame[off+3] = 0xFF
				}
			}
		}
	}
}
