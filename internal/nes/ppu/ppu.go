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
	"sync"

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

	// VRAM addressing latches per the nesdev "loopy" register layout
	// (issue #268). Both v and t are 15-bit; their layout is:
	//
	//   yyy NN YYYYY XXXXX
	//   ||| || ||||| +++++-- coarse X scroll (5 bits)
	//   ||| || +++++-------- coarse Y scroll (5 bits)
	//   ||| ++-------------- nametable select (2 bits)
	//   +++----------------- fine Y scroll (3 bits)
	//
	// fineX (x) is a separate 3-bit latch that picks the leftmost
	// rendered bit of the prefetched pattern row. w is the shared
	// write-toggle for $2005 + $2006 (cleared by $2002 reads).
	v       uint16 // current VRAM address — drives $2007 + per-dot fetches
	t       uint16 // temp VRAM address — staged for the next render
	x       byte   // fine X scroll (3 bits)
	w       bool   // $2005 / $2006 write toggle
	readBuf byte   // $2007 read returns the previously-buffered byte
	// scrollX / scrollY are the legacy per-frame snapshot path —
	// kept alongside the loopy latches until renderScanline migrates
	// to per-dot v reads (later commit in this branch).
	scrollX  byte
	scrollY  byte
	scrollHi bool // tracks $2005 toggle state alongside $2006

	// Memory the PPU owns directly.
	vram    [0x800]byte // 2 KiB nametable RAM
	oam     [256]byte   // sprite memory
	palette [32]byte    // palette RAM

	// Timing.
	scanline   int
	dot        int
	frameCount uint64

	// Framebuffer pair. `frame` is the back / work buffer that
	// per-scanline render writes to. `displayFrame` is the
	// presentation buffer Ebiten's Draw goroutine reads from via
	// FrameBuffer(). At vblank entry the back buffer copies into
	// displayFrame so Draw always sees a fully-rendered frame
	// regardless of where the emulator's per-dot stepping is.
	// Without the split, Ebiten's multi-thread mode could sample
	// `frame` while only the top N scanlines had rendered this
	// frame → visible horizontal tear.
	frame        [ScreenWidth * ScreenHeight * 4]byte
	displayFrame [ScreenWidth * ScreenHeight * 4]byte
	displayMu    sync.Mutex

	// bgOpaque mirrors `frame` at 1 bool per pixel and records whether
	// the BG plane wrote a non-zero (i.e. opaque) palette index there.
	// renderSprites consults this for sprite-0 hit detection and for
	// the priority-bit "sprite behind BG" composite rule. Populated
	// by renderFrame; consumed by renderSprites within the same vblank
	// pass.
	bgOpaque [ScreenWidth * ScreenHeight]bool

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
	p := &PPU{cart: cart, nmi: nmi}
	p.Reset()
	return p
}

// Reset clears the PPU to a deterministic post-power state. Real
// silicon's reset behavior is a bit fuzzier (writes ignored for a
// short window, etc.) — chippy follows the convention emulators settle
// on.
func (p *PPU) Reset() {
	p.ctrl, p.mask, p.status, p.oamAddr = 0, 0, 0, 0
	p.v, p.t, p.x, p.w, p.readBuf = 0, 0, 0, false, 0
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
		// Per nesdev: $2000 writes update t's nametable-select bits
		// (10-11) from data bits 0-1.
		p.t = (p.t & 0xF3FF) | (uint16(v&0x03) << 10)
		// Mid-frame nametable swap also logs a scroll event so the
		// legacy per-scanline snapshot path captures the change.
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
		if !p.w {
			// First write: t coarseX = data >> 3; x = data & 7.
			p.t = (p.t & 0xFFE0) | uint16(v>>3)
			p.x = v & 0x07
			p.scrollX = v
			p.w = true
		} else {
			// Second write: t fineY (bits 12-14) = data & 7;
			// t coarseY (bits 5-9) = data >> 3.
			p.t = (p.t & 0x8C1F) |
				(uint16(v&0x07) << 12) |
				(uint16(v&0xF8) << 2)
			p.scrollY = v
			p.w = false
		}
		// Keep the legacy scrollHi toggle in sync until the snapshot
		// path is fully retired by the per-dot v reads.
		p.scrollHi = p.w
		p.recordScrollChange()
	case 0x2006:
		if !p.w {
			// First write: t high byte = data & $3F (bit 14 cleared
			// per nesdev). Don't touch v yet.
			p.t = (p.t & 0x00FF) | (uint16(v&0x3F) << 8)
			p.t &= 0x7FFF  // ensure bit 15 stays 0 (15-bit register)
			p.t &^= 0x4000 // bit 14 cleared by $2006 first write
			p.w = true
		} else {
			// Second write: t low byte = data; then v ← t.
			p.t = (p.t & 0xFF00) | uint16(v)
			p.v = p.t
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

// checkSprite0HitForScanline scans sprite 0's row intersecting y
// and sets $2002 bit 6 the moment an opaque sprite-0 pixel
// overlaps an opaque BG pixel. Called from stepDot at each visible
// scanline so the flag latches at the actual hit scanline, not a
// per-frame post-hoc pass. Idempotent — once set, the flag stays
// latched until end-of-vblank's status clear.
func (p *PPU) checkSprite0HitForScanline(y int) {
	if p.status&0x40 != 0 {
		return
	}
	if p.mask&0x18 != 0x18 {
		return
	}
	spriteY := int(p.oam[0]) + 1
	spriteH := 8
	if p.ctrl&0x20 != 0 {
		spriteH = 16
	}
	if y < spriteY || y >= spriteY+spriteH {
		return
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
	spLo := p.busRead(tileAddr)
	spHi := p.busRead(tileAddr + 8)

	// Resolve the active scroll snapshot at THIS scanline (walks
	// the scrollEvents list — sprite-0 hits typically fire near the
	// top of the frame so the cursor stays close to index 0).
	snap := p.activeScrollFor(y)

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
		effX := px + int(snap.scrollX)
		effY := y + int(snap.scrollY)
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
		if ((bgLo>>bgB)&1)|((bgHi>>bgB)&1) != 0 {
			p.status |= 0x40
			return
		}
	}
}

// activeScrollFor returns the scrollSnapshot in effect at the
// given visible scanline — the latest event with scanline <= y,
// or frameStartScroll if no events come before y.
func (p *PPU) activeScrollFor(y int) scrollSnapshot {
	active := p.frameStartScroll
	for _, ev := range p.scrollEvents {
		if ev.scanline > y {
			break
		}
		active = ev
	}
	return active
}

// incCoarseX advances v's coarse-X (bits 0-4) wrapping at 32, where
// the wrap also flips the nametable's horizontal bit (10). Fired at
// dots 8, 16, ..., 256 of visible + pre-render scanlines while
// rendering is enabled (per nesdev "loopy" timing diagram).
func (p *PPU) incCoarseX() {
	if p.v&0x001F == 31 {
		p.v &^= 0x001F
		p.v ^= 0x0400 // flip horizontal nametable bit
	} else {
		p.v++
	}
}

// incY advances v's fine-Y (bits 12-14) and coarse-Y (bits 5-9)
// with the standard 2C02 carry: fine-Y rolls every 8 lines, coarse-Y
// rolls at 30 (the visible nametable height — rows 30-31 are the
// attribute table, not displayable) and flips the vertical
// nametable bit (11). Fired at dot 256 of visible + pre-render
// scanlines while rendering is enabled.
func (p *PPU) incY() {
	if p.v&0x7000 != 0x7000 {
		p.v += 0x1000 // fine-Y < 7: bump
	} else {
		p.v &^= 0x7000 // fine-Y wraps to 0
		y := (p.v & 0x03E0) >> 5
		switch y {
		case 29:
			y = 0
			p.v ^= 0x0800 // flip vertical nametable bit
		case 31:
			y = 0 // attribute-table rollover; no nametable flip
		default:
			y++
		}
		p.v = (p.v &^ 0x03E0) | (y << 5)
	}
}

// copyXFromT copies the horizontal bits of t into v (coarse-X +
// horizontal nametable bit). Per nesdev, fired at dot 257 of every
// visible + pre-render scanline when rendering is enabled.
func (p *PPU) copyXFromT() {
	p.v = (p.v &^ 0x041F) | (p.t & 0x041F)
}

// copyYFromT copies the vertical bits of t into v (fine-Y, coarse-Y,
// vertical nametable bit). Per nesdev, fired repeatedly at dots
// 280-304 of the pre-render scanline when rendering is enabled.
func (p *PPU) copyYFromT() {
	p.v = (p.v &^ 0x7BE0) | (p.t & 0x7BE0)
}

// renderingEnabled reports whether the PPU is currently rendering
// (PPUMASK BG show OR sprite show). Loopy v register increments
// only fire when rendering is on — disabled rendering = no fetches
// = no scrolling state machine. cmd/nessy hits PPUMASK bit 3 + 4
// for the playfield; status-bar-only states still count as
// "rendering" if either bit is on.
func (p *PPU) renderingEnabled() bool {
	return p.mask&0x18 != 0
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
		}
	}
	// Per-scanline sprite-0 hit detector (issue #268). At each
	// visible scanline we live-check whether sprite 0's row at y
	// overlaps an opaque BG pixel; first overlap latches $2002 bit
	// 6 at the actual scanline of the hit, no frame-start
	// prediction needed. Fires at dot 1 so the game's poll loop
	// (which runs from the just-emitted vblank/end-of-prev-scanline
	// NMI / opcodes) sees the flag in time for the mid-frame scroll
	// write.
	if p.dot == 1 && p.scanline >= 0 && p.scanline < ScreenHeight {
		p.checkSprite0HitForScanline(p.scanline)
	}
	// Per-scanline BG render + sprite composite (issue #268). Each
	// visible scanline rasterizes at dot 256: BG first, then
	// sprites composited over it. Combining the passes here means
	// Ebiten's Draw can sample the framebuffer at any moment and
	// always sees a "complete" scanline (BG + sprites for that y)
	// — the previous per-frame-only sprite composite left a window
	// during which scanlines had BG but no sprites yet, causing
	// visible flicker / "sprites erased by scanline" reports.
	if p.dot == 256 && p.scanline >= 0 && p.scanline < ScreenHeight {
		p.renderScanlineEnabled(p.scanline)
		p.compositeScanlineSprites(p.scanline)
	}
	// Loopy v register increments per nesdev's PPU timing diagram
	// (issue #268 stage 3). Only fire when rendering is on. Visible
	// scanlines + pre-render scanline run the same fetch state
	// machine; vblank scanlines do nothing.
	if p.renderingEnabled() &&
		(p.scanline < ScreenHeight || p.scanline == preRenderScanline) {
		switch {
		case p.dot >= 1 && p.dot <= 256 && p.dot%8 == 0:
			// Tile fetch boundary — coarse-X bumps every 8 dots
			// from dot 8 through dot 256.
			p.incCoarseX()
			if p.dot == 256 {
				// Dot 256 also triggers the Y increment (the dot
				// after the last visible-pixel tile fetch).
				p.incY()
			}
		case p.dot == 257:
			// Horizontal reload: t's coarse-X + horizontal NT bit
			// copy into v so the next scanline's tile fetches start
			// from the freshly-set scroll-X.
			p.copyXFromT()
		case p.scanline == preRenderScanline && p.dot >= 280 && p.dot <= 304:
			// Vertical reload: t's fine-Y + coarse-Y + vertical NT
			// bit copy into v during pre-render. Real silicon
			// repeats the copy across 25 dots; the result is
			// idempotent so a single copy at this range suffices.
			p.copyYFromT()
		}
	}
	switch {
	case p.scanline == vblankScanline && p.dot == 1:
		// Per-scanline render already painted every visible scanline
		// at its dot 256. At vblank entry we publish the back buffer
		// to the presentation buffer (atomic copy under displayMu)
		// so Ebiten's Draw goroutine always sees a complete frame,
		// then flush per-frame state + raise vblank + fire NMI.
		p.PresentFrame()
		p.scrollEvents = p.scrollEvents[:0]
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

// FrameBuffer returns a 256 × 240 RGBA byte slice. Indexed row-
// major, 4 bytes per pixel (R, G, B, A). Returns the presentation
// buffer — atomically swapped at vblank entry from the back buffer
// the per-scanline render writes to. Safe to read from any
// goroutine without coordinating with stepDot.
//
// Tests that call renderFrame() directly (bypassing the emulator
// step loop) need to call PresentFrame() explicitly afterwards to
// publish the back buffer; otherwise FrameBuffer returns whatever
// was last published.
func (p *PPU) FrameBuffer() []byte {
	p.displayMu.Lock()
	defer p.displayMu.Unlock()
	out := make([]byte, len(p.displayFrame))
	copy(out, p.displayFrame[:])
	return out
}

// PresentFrame copies the back framebuffer into the presentation
// buffer. Called from stepDot at vblank entry; also exposed so
// tests calling renderFrame() directly can flush the result.
func (p *PPU) PresentFrame() {
	p.displayMu.Lock()
	copy(p.displayFrame[:], p.frame[:])
	p.displayMu.Unlock()
}

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
