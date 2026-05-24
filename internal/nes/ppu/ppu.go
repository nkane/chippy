// Package ppu models the NES Picture Processing Unit (2C02 / 2C07).
//
// v0.1 ships background-only rendering. The PPU advances three dots per
// CPU cycle, walks the 341 × 262 NTSC frame timing diagram, sets the
// vblank flag at scanline 241 dot 1 (with NMI if PPUCTRL bit 7 is set),
// and at that boundary renders the visible 256 × 240 region to an RGBA
// framebuffer using the nametable / attribute / pattern-table state in
// effect at vblank entry.
//
// Out of scope for v0.1 (deliberately deferred):
//   - Sprites (OAM rendering, sprite-0 hit, sprite overflow)
//   - $4014 OAMDMA — the byte copy is straightforward but it requires
//     a CPU "stall N cycles" hook that doesn't exist yet (#175 added
//     the inbound Ticker direction, not outbound stall).
//   - Mid-frame scrolling and the v/t/x/w internal-latch state machine.
//   - Greyscale and color-emphasis bits (PPUMASK bits 0, 5-7).
//   - Pre-render scanline odd-frame dot-skip.
//
// These cost real ROMs accuracy on dynamic / scrolling games, but v0.1
// only commits to static title screens.
package ppu

import (
	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/nes"
)

const (
	ScreenWidth  = 256
	ScreenHeight = 240

	dotsPerScanline   = 341
	scanlinesPerFrame = 262
	vblankScanline    = 241
	preRenderScanline = 261
)

// Cart is the PPU-side view of the cartridge. cart.Cartridge satisfies
// this; the ppu package only needs the PPU bus and the mirroring scheme.
type Cart interface {
	PPURead(addr uint16) byte
	PPUWrite(addr uint16, v byte)
	Mirroring() nes.Mirroring
}

// NMI is the CPU's edge-triggered non-maskable-interrupt line. *cpu.CPU
// satisfies it via TriggerNMI(). Kept as an interface so tests can
// assert NMI was raised without spinning up a full CPU.
type NMI interface {
	TriggerNMI()
}

// PPU is the Peripheral that claims $2000-$3FFF on the CPU bus. The
// 8-byte register window at $2000-$2007 is mirrored every 8 bytes up
// to $3FFF.
type PPU struct {
	cart Cart
	nmi  NMI

	// Latched registers ($2000-$2007).
	ctrl    byte // $2000 PPUCTRL
	mask    byte // $2001 PPUMASK
	status  byte // $2002 PPUSTATUS (bit 7 = vblank)
	oamAddr byte // $2003 OAMADDR

	// VRAM addressing latches. The 2C02 has a 15-bit "v" current
	// address used by $2007 access (and rendering, on real silicon).
	// $2005 / $2006 share a single write-toggle "w" — first write sets
	// the high half, second write sets the low half. v0.1 ignores the
	// fine X / scroll subtleties since we don't model mid-frame
	// scrolling; only "v" matters for $2007 r/w.
	v        uint16
	w        bool
	readBuf  byte // $2007 read returns the previously-buffered byte
	scrollX  byte // most-recent $2005 X (snapshotted at frame render)
	scrollY  byte // most-recent $2005 Y
	scrollHi bool // tracks $2005 toggle state alongside $2006

	// Memory the PPU owns directly.
	vram    [0x800]byte // 2 KiB nametable RAM
	oam     [256]byte   // sprite memory
	palette [32]byte    // palette RAM

	// Timing.
	scanline   int
	dot        int
	frameCount uint64

	// Framebuffer: 256 × 240 RGBA. Rendered at vblank entry; presented
	// to the host via Frame().
	frame [ScreenWidth * ScreenHeight * 4]byte

	// bgOpaque mirrors `frame` at 1 bool per pixel and records whether
	// the BG plane wrote a non-zero (i.e. opaque) palette index there.
	// renderSprites consults this for sprite-0 hit detection and for
	// the priority-bit "sprite behind BG" composite rule. Populated
	// by renderFrame; consumed by renderSprites within the same vblank
	// pass.
	bgOpaque [ScreenWidth * ScreenHeight]bool

	// sprite0HitScanline (-1 = no hit predicted) is the visible
	// scanline at which the next frame's sprite-0 hit will land,
	// computed from THIS frame's renderSprites pass. stepDot fires
	// the $2002 bit-6 flag when scanline reaches this value so
	// games that poll $2002 for the hit (SMB1 status-bar split)
	// see the flag mid-frame instead of waiting until the per-
	// frame compositor runs at vblank entry. Per-dot accuracy is
	// the v0.4 #268 issue; this is the v0.3 stopgap.
	sprite0HitScanline int

	// Scroll capture for mid-frame splits (issue #206). frameStartScroll
	// is snapshotted at the end of vblank (when scanline rolls back to
	// 0) so renderFrame knows what scroll values were active for the
	// scanlines BEFORE any mid-frame $2005 / $2006 / $2000 writes.
	// scrollEvents records every such write that occurs during visible
	// scanlines 0..239; renderFrame walks them in order to derive the
	// active snapshot per scanline. Reset after renderFrame consumes
	// them. SMB1's status-bar split uses exactly this surface.
	frameStartScroll scrollSnapshot
	scrollEvents     []scrollSnapshot
}

// scrollSnapshot bundles the scroll-relevant state captured at a
// specific scanline. baseNametable holds PPUCTRL bits 0-1; scrollX /
// scrollY are the most recent $2005 writes (or $2006 dervied
// equivalents — game-of-life scroll updates).
type scrollSnapshot struct {
	scanline      int
	scrollX       byte
	scrollY       byte
	baseNametable byte // PPUCTRL bits 0-1
}

// New constructs a PPU wired to a cartridge (for PPU-bus pattern-table
// access + nametable mirroring) and an NMI sink (the CPU). Both may be
// nil for register-level tests that don't exercise rendering or NMI.
func New(cart Cart, nmi NMI) *PPU {
	p := &PPU{cart: cart, nmi: nmi, sprite0HitScanline: -1}
	p.Reset()
	return p
}

// Reset clears the PPU to a deterministic post-power state. Real
// silicon's reset behavior is a bit fuzzier (writes ignored for a
// short window, etc.) — chippy follows the convention emulators settle
// on.
func (p *PPU) Reset() {
	p.ctrl, p.mask, p.status, p.oamAddr = 0, 0, 0, 0
	p.v, p.w, p.readBuf = 0, false, 0
	p.scrollX, p.scrollY, p.scrollHi = 0, 0, false
	p.scanline, p.dot, p.frameCount = preRenderScanline, 0, 0
	for i := range p.vram {
		p.vram[i] = 0
	}
	for i := range p.oam {
		p.oam[i] = 0
	}
	for i := range p.palette {
		p.palette[i] = 0
	}
	for i := range p.frame {
		p.frame[i] = 0
	}
	p.sprite0HitScanline = -1
}

// Range claims $2000-$3FFF (the mirrored register window). $4014
// OAMDMA is deliberately not claimed here — see package doc.
func (p *PPU) Range() (uint16, uint16) { return 0x2000, 0x3FFF }

// Read services CPU reads of mirrored PPU registers.
func (p *PPU) Read(addr uint16) byte {
	switch 0x2000 | (addr & 0x0007) {
	case 0x2002:
		// PPUSTATUS: top 3 bits return the live flags; the bottom 5 are
		// "open-bus" — many ROMs see the last data on the PPU bus there,
		// but for v0.1 we return 0. Reading also clears vblank (bit 7)
		// and resets the $2005 / $2006 write-toggle.
		s := p.status & 0xE0
		p.status &^= 0x80
		p.w = false
		p.scrollHi = false
		return s
	case 0x2004:
		return p.oam[p.oamAddr]
	case 0x2007:
		// $2007 reads are buffered: each read returns the previously
		// buffered byte, then refills the buffer from VRAM[v]. Reads
		// from palette space ($3F00-$3FFF) bypass the buffer and
		// return immediately; the buffer is loaded with the mirrored
		// nametable byte underneath the palette region.
		var out byte
		addrV := p.v & 0x3FFF
		if addrV >= 0x3F00 {
			out = p.busRead(addrV)
			p.readBuf = p.busRead(addrV - 0x1000)
		} else {
			out = p.readBuf
			p.readBuf = p.busRead(addrV)
		}
		p.incVRAMAddr()
		return out
	}
	return 0
}

// Write services CPU writes to mirrored PPU registers.
func (p *PPU) Write(addr uint16, v byte) {
	switch 0x2000 | (addr & 0x0007) {
	case 0x2000:
		prev := p.ctrl
		p.ctrl = v
		// 2C02 quirk: setting PPUCTRL bit 7 while vblank is already
		// pending triggers an immediate NMI. Important for some games
		// (and a known nestest probe).
		if prev&0x80 == 0 && v&0x80 != 0 && p.status&0x80 != 0 && p.nmi != nil {
			p.nmi.TriggerNMI()
		}
		// Mid-frame nametable swap (bits 0-1) — log so renderFrame can
		// honor split-screen tricks that change the base nametable
		// mid-render. v0.1 captured these per-frame; v0.2 events drive
		// the per-scanline path.
		if prev&0x03 != v&0x03 {
			p.recordScrollChange()
		}
	case 0x2001:
		p.mask = v
	case 0x2003:
		p.oamAddr = v
	case 0x2004:
		p.oam[p.oamAddr] = v
		p.oamAddr++
	case 0x2005:
		if !p.scrollHi {
			p.scrollX = v
			p.scrollHi = true
		} else {
			p.scrollY = v
			p.scrollHi = false
		}
		p.recordScrollChange()
	case 0x2006:
		if !p.w {
			// First write: high byte of 15-bit address. Real silicon
			// stores in the "t" temp latch; v0.1 just builds v
			// directly since we don't model mid-frame fine-scroll.
			p.v = (p.v & 0x00FF) | (uint16(v&0x3F) << 8)
			p.w = true
		} else {
			p.v = (p.v & 0xFF00) | uint16(v)
			p.w = false
			// $2006's second write commits a fresh VRAM address that
			// the rendering pipeline reads coarse-X / coarse-Y /
			// nametable bits out of. SMB1 uses $2006 mid-frame to
			// reset scroll for its status-bar split.
			p.scrollFromV()
			p.recordScrollChange()
		}
	case 0x2007:
		p.busWrite(p.v&0x3FFF, v)
		p.incVRAMAddr()
	}
}

// recordScrollChange appends a snapshot of the current scroll state
// to the per-frame events log — but only when the change happens
// during a visible scanline (0..239). Writes during vblank
// (240..261) become the next frame's starting scroll instead and
// are captured by stepDot's frame-start snapshot path. Same goes
// for the initial pre-scanline boot: no event log spam before the
// first frame starts.
func (p *PPU) recordScrollChange() {
	if p.scanline < 0 || p.scanline >= ScreenHeight {
		return
	}
	p.scrollEvents = append(p.scrollEvents, scrollSnapshot{
		scanline:      p.scanline,
		scrollX:       p.scrollX,
		scrollY:       p.scrollY,
		baseNametable: p.ctrl & 0x03,
	})
}

// scrollFromV synthesizes scrollX / scrollY / baseNametable from the
// current 15-bit `v` latch. SMB1 sets scroll mid-frame via $2006
// pairs (not $2005), so we have to derive the effective scroll from
// `v`'s coarse + fine bits per the nesdev "loopy" layout:
//
//	yyy NN YYYYY XXXXX
//	||| || ||||| +++++-- coarse X (5 bits)
//	||| || +++++-------- coarse Y (5 bits)
//	||| ++-------------- nametable select (2 bits)
//	+++----------------- fine Y (3 bits)
//
// fine X is NOT stored in v (it lives in the separate `x` latch);
// v0.2 doesn't model fine-X yet, so scrollX is whatever coarse-X*8
// $2006 implicitly latched.
func (p *PPU) scrollFromV() {
	coarseX := byte(p.v & 0x1F)
	coarseY := byte((p.v >> 5) & 0x1F)
	fineY := byte((p.v >> 12) & 0x07)
	nametable := byte((p.v >> 10) & 0x03)
	p.scrollX = coarseX * 8
	p.scrollY = coarseY*8 + fineY
	p.ctrl = (p.ctrl &^ 0x03) | nametable
}

// predictSprite0Hit computes the visible scanline at which the
// $2002 bit-6 flag should fire this frame by walking sprite 0's
// 8×8 (or 8×16) bounding box against the current frame's BG state
// — nametable + attribute + pattern fetched live from the cart.
// Stores -1 when no hit will occur (either gating disabled or no
// opaque-over-opaque overlap exists).
//
// Called once per frame at the scanline 0 dot 0 transition, before
// the game's NMI handler has finished any post-vblank work but
// after any prior-frame nametable writes have settled. Uses
// frameStartScroll for the BG-pixel resolution so the prediction
// matches what the playfield will look like at scanlines BEFORE
// the mid-frame split.
//
// Per-dot accuracy (the full v0.4 #268 path) eventually replaces
// this; for v0.3 the prediction is sufficient for SMB1-class
// titles where sprite-0 sits in the static status bar.
func (p *PPU) predictSprite0Hit() {
	p.sprite0HitScanline = -1
	if p.mask&0x18 != 0x18 {
		return
	}
	spriteY := int(p.oam[0]) + 1
	if spriteY >= ScreenHeight {
		return
	}
	spriteH := 8
	if p.ctrl&0x20 != 0 {
		spriteH = 16
	}
	tileIdx := p.oam[1]
	attr := p.oam[2]
	spriteX := int(p.oam[3])
	hflip := attr&0x40 != 0
	vflip := attr&0x80 != 0
	sprPatternBase := uint16(0)
	if p.ctrl&0x08 != 0 && p.ctrl&0x20 == 0 {
		sprPatternBase = 0x1000
	}
	bgPatternBase := uint16(0)
	if p.ctrl&0x10 != 0 {
		bgPatternBase = 0x1000
	}
	snap := p.frameStartScroll

	for row := 0; row < spriteH; row++ {
		py := spriteY + row
		if py >= ScreenHeight {
			break
		}
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
		spLo := p.busRead(tileAddr)
		spHi := p.busRead(tileAddr + 8)
		for col := 0; col < 8; col++ {
			px := spriteX + col
			if px < 0 || px >= ScreenWidth {
				continue
			}
			bitCol := col
			if !hflip {
				bitCol = 7 - col
			}
			b := uint(bitCol)
			if ((spLo>>b)&1)|((spHi>>b)&1) == 0 {
				continue
			}
			// BG lookup at (px, py) using frameStartScroll.
			effX := px + int(snap.scrollX)
			effY := py + int(snap.scrollY)
			ntX := snap.baseNametable & 1
			ntY := (snap.baseNametable >> 1) & 1
			if effX >= 256 {
				effX -= 256
				ntX ^= 1
			}
			if effY >= 240 {
				effY -= 240
				ntY ^= 1
			}
			coarseX := effX / 8
			coarseY := effY / 8
			fineX := effX % 8
			fineYbg := effY % 8
			ntBase := uint16(0x2000) +
				uint16(ntY)*0x0800 +
				uint16(ntX)*0x0400
			bgTileIdx := p.busRead(ntBase + uint16(coarseY)*32 + uint16(coarseX))
			bgAddr := bgPatternBase + uint16(bgTileIdx)*16 + uint16(fineYbg)
			bgLo := p.busRead(bgAddr)
			bgHi := p.busRead(bgAddr + 8)
			bgB := uint(7 - fineX)
			bgLow := (bgLo >> bgB) & 1
			bgHigh := (bgHi >> bgB) & 1
			if (bgHigh<<1)|bgLow != 0 {
				p.sprite0HitScanline = py
				return
			}
		}
	}
}

// incVRAMAddr advances v after a $2007 access. PPUCTRL bit 2 selects
// step 1 vs step 32.
func (p *PPU) incVRAMAddr() {
	if p.ctrl&0x04 != 0 {
		p.v += 32
	} else {
		p.v++
	}
	p.v &= 0x3FFF
}

// Tick advances the PPU by 3 * cpuCycles dots — the 2C02 / 2A03 share a
// master clock, with the PPU running 3× the CPU's rate. Crosses scanline
// boundaries and triggers vblank / NMI at the right dot.
func (p *PPU) Tick(cpuCycles int) {
	for range cpuCycles * 3 {
		p.stepDot()
	}
}

func (p *PPU) stepDot() {
	p.dot++
	if p.dot >= dotsPerScanline {
		p.dot = 0
		p.scanline++
		if p.scanline >= scanlinesPerFrame {
			p.scanline = 0
			p.frameCount++
			// New frame begins. Snapshot the current scroll values so
			// renderFrame at this frame's eventual vblank entry knows
			// what was active for the scanlines that precede any
			// mid-frame $2005 / $2006 / $2000 writes.
			p.frameStartScroll = scrollSnapshot{
				scanline:      0,
				scrollX:       p.scrollX,
				scrollY:       p.scrollY,
				baseNametable: p.ctrl & 0x03,
			}
			// Re-predict sprite-0 hit using CURRENT state. The prior
			// pass at renderSprites used last frame's bgOpaque — fine
			// when sprite-0 + BG are stable, broken the moment a new
			// BG column scrolls in. Predicting per-frame from the live
			// nametable + sprite-0 OAM keeps SMB1 status-bar splits
			// stable across the playfield.
			p.predictSprite0Hit()
		}
	}
	// Mid-frame sprite-0 hit predictor — see sprite0HitScanline.
	// Fires once per frame at the predicted scanline so games that
	// poll $2002 bit 6 in a tight loop (SMB1 status-bar split) see
	// the flag set mid-render instead of post-frame.
	if p.dot == 1 && p.scanline >= 0 && p.scanline < ScreenHeight &&
		p.scanline == p.sprite0HitScanline &&
		p.mask&0x18 == 0x18 && p.status&0x40 == 0 {
		p.status |= 0x40
	}
	switch {
	case p.scanline == vblankScanline && p.dot == 1:
		// Render the visible frame using state captured at vblank
		// entry, then raise vblank + NMI. BG layer first so the
		// sprite compositor can read bgOpaque for sprite-0 hit + the
		// priority-behind-BG rule.
		p.renderFrame()
		p.renderSprites()
		p.status |= 0x80
		if p.ctrl&0x80 != 0 && p.nmi != nil {
			p.nmi.TriggerNMI()
		}
	case p.scanline == preRenderScanline && p.dot == 1:
		// End of vblank / start of pre-render: clear vblank, sprite-0
		// hit, sprite overflow. v0.1 doesn't model the latter two so
		// they're always clear, but the mask covers their bits for
		// when they land.
		p.status &^= 0xE0
	}
}

// FrameBuffer returns a 256 × 240 RGBA byte slice. Indexed row-major,
// 4 bytes per pixel (R, G, B, A). The returned slice aliases the PPU's
// internal frame; callers must not mutate it. A fresh copy is rendered
// at vblank entry — calling FrameBuffer at any other time returns the
// most-recent frame.
func (p *PPU) FrameBuffer() []byte { return p.frame[:] }

// FrameCount is the number of frames rendered since reset. Tests use
// this to detect "we crossed a frame boundary".
func (p *PPU) FrameCount() uint64 { return p.frameCount }

// Status returns the current PPUSTATUS without the read side effect of
// clearing vblank. Exposed for tests and the debug TUI.
func (p *PPU) Status() byte { return p.status }

// Scanline / Dot expose the timing cursor. Useful for tests and a
// future PPU debug panel.
func (p *PPU) Scanline() int { return p.scanline }
func (p *PPU) Dot() int      { return p.dot }

// WriteOAM is the entry point for $4014 OAMDMA once it lands — copies a
// byte into OAM at the current oamAddr cursor and bumps. Exposed
// separately from $2004 so a future OAMDMA peripheral doesn't have to
// route through the mirrored register decoder.
func (p *PPU) WriteOAM(v byte) {
	p.oam[p.oamAddr] = v
	p.oamAddr++
}

// OAM peeks the sprite memory byte at index i. Side-effect-free —
// safe for tests and debugger introspection. Reads through the
// register window ($2004) bump oamAddr and serve open-bus quirks
// during rendering; OAM is the bypass.
func (p *PPU) OAM(i byte) byte { return p.oam[i] }

// compile-time check: PPU satisfies cpu.Peripheral.
var _ cpu.Peripheral = (*PPU)(nil)
