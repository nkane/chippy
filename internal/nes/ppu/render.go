package ppu

// renderFrame fills the 256 × 240 RGBA framebuffer with the background
// layer. v0.2 ships a per-scanline path (issue #206) so mid-frame
// $2000 / $2005 / $2006 writes alter the visible scroll for the rows
// that follow — the SMB1 status-bar split is the canonical use.
//
// State sources:
//
//   - frameStartScroll: snapshotted in stepDot when scanline rolls
//     to 0. Holds whatever the game wrote during the previous
//     vblank.
//   - scrollEvents: appended by Write whenever $2000/$2005/$2006
//     changes scroll-relevant state during a visible scanline. Each
//     event carries the scanline it landed on; renderFrame walks
//     them in order to derive the active snapshot per row.
//
// Per-pixel cost is bounded by 4 PPU-bus reads (nametable +
// attribute + low pattern + high pattern). Tile-row caching is a
// perf knob to revisit if the perfgate bench complains; v0.2 ships
// the simple version.
//
// Out of scope: cycle-accurate v/t/x/w per-dot replication (fine-X
// bit slide, horizontal-bit copy at dot 257, Y reload during
// pre-render dots 280-304). Per-scanline + per-tile fetch is
// sufficient for SMB1-class games — see the issue's "out of scope"
// notes.
func (p *PPU) renderFrame() {
	// Reset bgOpaque before drawing. renderSprites reads it and every
	// frame starts from a clean slate.
	for i := range p.bgOpaque {
		p.bgOpaque[i] = false
	}
	if p.mask&0x08 == 0 {
		// PPUMASK bit 3 = "show background". When off the screen is
		// the universal background color and bgOpaque stays
		// all-false so sprites win every composite.
		r, g, b := paletteRGB(p.palette[0])
		for i := 0; i < len(p.frame); i += 4 {
			p.frame[i+0] = r
			p.frame[i+1] = g
			p.frame[i+2] = b
			p.frame[i+3] = 0xFF
		}
		p.scrollEvents = p.scrollEvents[:0]
		return
	}

	// Walk scanlines, advancing the events cursor as we cross each
	// snapshot's scanline. Activity below the snapshot's scanline
	// renders with the *previous* snapshot — events are inclusive at
	// the snapshot's scanline (e.g. a write at scanline 32 applies
	// from row 32 down).
	active := p.frameStartScroll
	eventIdx := 0
	for y := range ScreenHeight {
		for eventIdx < len(p.scrollEvents) && p.scrollEvents[eventIdx].scanline <= y {
			active = p.scrollEvents[eventIdx]
			eventIdx++
		}
		p.renderScanline(y, active)
	}
	// Done consuming this frame's events. Next frame starts with a
	// clean log + a fresh frameStartScroll captured by stepDot.
	p.scrollEvents = p.scrollEvents[:0]
}

// renderScanline rasterizes one row using the supplied snapshot's
// scroll values. The horizontal axis walks every visible pixel
// (256); per-pixel cost is 4 PPU-bus reads worst case (nametable,
// attribute, two pattern planes). Adjacent pixels within the same
// 8×8 source tile reuse all four — we lazily cache the last tile's
// fetch via the cursorTile local. PPU mirroring (cart.Mirroring) is
// handled inside busRead.
func (p *PPU) renderScanline(y int, snap scrollSnapshot) {
	patternBase := uint16(0)
	if p.ctrl&0x10 != 0 {
		patternBase = 0x1000
	}

	type tileCache struct {
		valid       bool
		coarseX     int
		coarseY     int
		nametableX  byte
		nametableY  byte
		fineY       int
		paletteSel  byte
		patternLow  byte
		patternHigh byte
	}
	var cur tileCache

	baseNTX := snap.baseNametable & 1
	baseNTY := (snap.baseNametable >> 1) & 1
	scrollX := int(snap.scrollX)
	scrollY := int(snap.scrollY)

	rowBase := y * ScreenWidth * 4
	for x := range ScreenWidth {
		// Effective source pixel after applying scroll. Horizontal
		// overflow wraps into the adjacent nametable (PPUCTRL bit 0
		// flip); vertical overflow wraps into the next vertical
		// nametable (bit 1 flip).
		effX := x + scrollX
		effY := y + scrollY
		ntX := baseNTX
		ntY := baseNTY
		if effX >= 256 {
			effX -= 256
			ntX ^= 1
		}
		// Nametables are 30 tiles tall (240 px) of valid data + 2
		// tiles of attribute table padding. Vertical wrap at 240,
		// matching real silicon.
		if effY >= 240 {
			effY -= 240
			ntY ^= 1
		}
		coarseX := effX / 8
		coarseY := effY / 8
		fineX := effX % 8
		fineY := effY % 8

		if !cur.valid ||
			cur.coarseX != coarseX ||
			cur.coarseY != coarseY ||
			cur.nametableX != ntX ||
			cur.nametableY != ntY ||
			cur.fineY != fineY {
			ntBase := uint16(0x2000) +
				uint16(ntY)*0x0800 +
				uint16(ntX)*0x0400
			ntAddr := ntBase + uint16(coarseY)*32 + uint16(coarseX)
			tileIdx := p.busRead(ntAddr)
			attrAddr := ntBase + 0x03C0 +
				uint16(coarseY/4)*8 +
				uint16(coarseX/4)
			attr := p.busRead(attrAddr)
			quadrantShift := uint(((coarseY%4)/2)*4 + ((coarseX%4)/2)*2)
			cur = tileCache{
				valid:       true,
				coarseX:     coarseX,
				coarseY:     coarseY,
				nametableX:  ntX,
				nametableY:  ntY,
				fineY:       fineY,
				paletteSel:  (attr >> quadrantShift) & 0x03,
				patternLow:  p.busRead(patternBase + uint16(tileIdx)*16 + uint16(fineY)),
				patternHigh: p.busRead(patternBase + uint16(tileIdx)*16 + uint16(fineY) + 8),
			}
		}

		bit := uint(7 - fineX)
		lo := (cur.patternLow >> bit) & 1
		hi := (cur.patternHigh >> bit) & 1
		val := (hi << 1) | lo

		var colorIdx byte
		if val == 0 {
			colorIdx = p.palette[0]
		} else {
			colorIdx = p.palette[(cur.paletteSel<<2)|val]
		}
		r, g, b := paletteRGB(colorIdx)
		off := rowBase + x*4
		p.frame[off+0] = r
		p.frame[off+1] = g
		p.frame[off+2] = b
		p.frame[off+3] = 0xFF
		p.bgOpaque[y*ScreenWidth+x] = val != 0
	}
}
