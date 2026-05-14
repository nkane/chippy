package ppu

import (
	"testing"

	"github.com/nkane/chippy/internal/nes"
)

// fakeCart is a deterministic Cart for tests: pattern table backed by a
// caller-supplied slice; mirroring controllable; nametable / palette
// live in the PPU itself, so the cart only owns the $0000-$1FFF window.
type fakeCart struct {
	chr [0x2000]byte
	mir nes.Mirroring
}

func (c *fakeCart) PPURead(addr uint16) byte     { return c.chr[addr&0x1FFF] }
func (c *fakeCart) PPUWrite(addr uint16, v byte) { c.chr[addr&0x1FFF] = v }
func (c *fakeCart) Mirroring() nes.Mirroring     { return c.mir }

// fakeNMI counts how many times TriggerNMI was called.
type fakeNMI struct{ count int }

func (n *fakeNMI) TriggerNMI() { n.count++ }

// Vblank flag flips at scanline 241 dot 1. Tick advances 3 dots per
// CPU cycle, so the precise CPU cycle count is the scanline / 3 + dot
// rounding.
func TestPPU_VblankAtScanline241(t *testing.T) {
	nmi := &fakeNMI{}
	p := New(&fakeCart{}, nmi)
	p.Write(0x2000, 0x80) // PPUCTRL bit 7: enable NMI

	// Step until scanline 241 dot 1. Dots-per-frame at vblank entry =
	// 241 * 341 + 1 = 82182 dots. cpuCycles = ceil(82182 / 3) = 27394.
	// Just tick a generous slice and inspect.
	for range 30000 {
		p.Tick(1)
		if p.Status()&0x80 != 0 {
			break
		}
	}
	if p.Status()&0x80 == 0 {
		t.Fatalf("vblank flag never set; scanline=%d dot=%d", p.Scanline(), p.Dot())
	}
	// One CPU cycle = 3 PPU dots, so we observe vblank within 3 dots
	// of the scanline-241 dot-1 boundary.
	if p.Scanline() != 241 || p.Dot() > 3 {
		t.Errorf("vblank fired at scanline=%d dot=%d; want scanline 241 dot ≤3", p.Scanline(), p.Dot())
	}
	if nmi.count != 1 {
		t.Errorf("NMI count = %d; want 1", nmi.count)
	}
}

// Vblank does not raise NMI when PPUCTRL bit 7 is clear.
func TestPPU_NoNMIWhenCtrlDisabled(t *testing.T) {
	nmi := &fakeNMI{}
	p := New(&fakeCart{}, nmi)
	// Tick through a full frame.
	for range 30000 {
		p.Tick(1)
	}
	if nmi.count != 0 {
		t.Errorf("NMI count = %d; want 0 with PPUCTRL bit 7 clear", nmi.count)
	}
}

// Reading $2002 clears vblank and resets the $2006 / $2005 latch.
func TestPPU_StatusReadClearsVblankAndLatch(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.status = 0x80
	p.w = true
	p.scrollHi = true
	got := p.Read(0x2002)
	if got&0x80 == 0 {
		t.Errorf("status read should return vblank bit set; got $%02X", got)
	}
	if p.status&0x80 != 0 {
		t.Errorf("status read must clear vblank flag")
	}
	if p.w || p.scrollHi {
		t.Errorf("status read must reset write toggle")
	}
}

// Setting PPUCTRL bit 7 while vblank is already pending triggers an
// immediate NMI (2C02 quirk; nestest probes this).
func TestPPU_LateNMIOnCtrlBit7Set(t *testing.T) {
	nmi := &fakeNMI{}
	p := New(&fakeCart{}, nmi)
	p.status = 0x80 // vblank already set
	p.Write(0x2000, 0x80)
	if nmi.count != 1 {
		t.Errorf("late NMI count = %d; want 1", nmi.count)
	}
}

// $2006 / $2007 round-trip with auto-increment 1.
func TestPPU_VRAMWriteReadStep1(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// Set v=$2000 via two-write latch.
	p.Write(0x2006, 0x20)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0xAB)
	p.Write(0x2007, 0xCD)
	// Now read back: $2007 reads are buffered (first read returns the
	// stale buffer, second the real byte). Re-point v to $2000:
	p.Write(0x2006, 0x20)
	p.Write(0x2006, 0x00)
	_ = p.Read(0x2007) // primes buffer with $2000
	if got := p.Read(0x2007); got != 0xAB {
		t.Errorf("VRAM[$2000] = $%02X; want $AB", got)
	}
	if got := p.Read(0x2007); got != 0xCD {
		t.Errorf("VRAM[$2001] = $%02X; want $CD", got)
	}
}

// $2006 / $2007 with PPUCTRL bit 2 set → auto-increment 32. After
// writing two bytes the bytes land at $2000 and $2020 respectively
// (each $2007 write stores at the current v, then bumps v by 32).
func TestPPU_VRAMAutoIncrement32(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2000, 0x04) // ctrl bit 2 = step 32
	p.Write(0x2006, 0x20)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x11) // VRAM[$2000] = $11, v → $2020
	p.Write(0x2007, 0x22) // VRAM[$2020] = $22, v → $2040

	// Read $2000 back.
	p.Write(0x2006, 0x20)
	p.Write(0x2006, 0x00)
	_ = p.Read(0x2007) // prime
	if got := p.Read(0x2007); got != 0x11 {
		t.Errorf("step-32 VRAM[$2000] = $%02X; want $11", got)
	}

	// Read $2020 back.
	p.Write(0x2006, 0x20)
	p.Write(0x2006, 0x20)
	_ = p.Read(0x2007) // prime
	if got := p.Read(0x2007); got != 0x22 {
		t.Errorf("step-32 VRAM[$2020] = $%02X; want $22", got)
	}
}

// $2007 palette reads bypass the buffer.
func TestPPU_PaletteReadIsImmediate(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// Set v=$3F00.
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x15) // palette[0] = $15
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	// Palette reads are immediate (no priming).
	if got := p.Read(0x2007); got != 0x15 {
		t.Errorf("palette read = $%02X; want $15 immediately (no buffer)", got)
	}
}

// $2005 PPUSCROLL toggles independently of internal latch state but
// honors the same write-toggle reset on $2002 read.
func TestPPU_ScrollLatchToggle(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2005, 0x42) // X = $42
	p.Write(0x2005, 0x37) // Y = $37
	if p.scrollX != 0x42 || p.scrollY != 0x37 {
		t.Errorf("scroll = ($%02X, $%02X); want ($42, $37)", p.scrollX, p.scrollY)
	}
	if p.scrollHi {
		t.Errorf("scroll toggle should be back to low after 2 writes")
	}
}

// Palette mirror: $3F10 / $3F14 / $3F18 / $3F1C mirror $3F00 / $3F04 / $3F08 / $3F0C.
func TestPaletteIndex_Mirrors(t *testing.T) {
	cases := []struct {
		addr uint16
		want uint16
	}{
		{0x3F00, 0x00},
		{0x3F10, 0x00},
		{0x3F14, 0x04},
		{0x3F18, 0x08},
		{0x3F1C, 0x0C},
		{0x3F11, 0x11}, // non-mirror entry stays put
		{0x3F1F, 0x1F},
		{0x3F20, 0x00}, // mirror of the whole 32-byte window
	}
	for _, c := range cases {
		if got := paletteIndex(c.addr); got != c.want {
			t.Errorf("paletteIndex($%04X) = $%02X; want $%02X", c.addr, got, c.want)
		}
	}
}

// Horizontal mirroring: nametables A A / B B → $2000=$2400, $2800=$2C00.
func TestPPU_NametableMirrorHorizontal(t *testing.T) {
	cart := &fakeCart{mir: nes.MirrorHorizontal}
	p := New(cart, nil)
	p.busWrite(0x2000, 0xAA)
	p.busWrite(0x2800, 0xBB)
	if got := p.busRead(0x2400); got != 0xAA {
		t.Errorf("$2400 mirrors $2000: got $%02X want $AA", got)
	}
	if got := p.busRead(0x2C00); got != 0xBB {
		t.Errorf("$2C00 mirrors $2800: got $%02X want $BB", got)
	}
}

// Vertical mirroring: nametables A B / A B → $2000=$2800, $2400=$2C00.
func TestPPU_NametableMirrorVertical(t *testing.T) {
	cart := &fakeCart{mir: nes.MirrorVertical}
	p := New(cart, nil)
	p.busWrite(0x2000, 0xAA)
	p.busWrite(0x2400, 0xBB)
	if got := p.busRead(0x2800); got != 0xAA {
		t.Errorf("$2800 mirrors $2000: got $%02X want $AA", got)
	}
	if got := p.busRead(0x2C00); got != 0xBB {
		t.Errorf("$2C00 mirrors $2400: got $%02X want $BB", got)
	}
}

// Synthetic CHR: one tile filled with pattern value 1 on every pixel.
// Place that tile at every nametable cell, set palette[1] to a known
// color, and verify the framebuffer is uniform.
func TestPPU_RendersUniformBackground(t *testing.T) {
	cart := &fakeCart{mir: nes.MirrorHorizontal}
	// Tile $00: low plane all 1s, high plane 0 → every pixel = 1.
	for i := range 8 {
		cart.chr[i+0] = 0xFF // low plane
		cart.chr[i+8] = 0x00 // high plane
	}
	p := New(cart, nil)
	// Enable BG show.
	p.Write(0x2001, 0x08)
	// Palette[0] (universal bg) = $0F (black). Palette[1] = $30 (white-ish).
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x0F)
	p.Write(0x2007, 0x30)
	// Nametable already zero-filled → every cell points to tile 0.
	// Render a frame.
	p.renderFrame()
	// All pixels should be palette[1] color → NES $30 → ~(0xFF, 0xFE, 0xFF).
	wantR, wantG, wantB := paletteRGB(0x30)
	for y := range ScreenHeight {
		for x := range ScreenWidth {
			off := (y*ScreenWidth + x) * 4
			if p.frame[off+0] != wantR || p.frame[off+1] != wantG || p.frame[off+2] != wantB {
				t.Fatalf("pixel ($%d, $%d) = (%02X,%02X,%02X); want (%02X,%02X,%02X)",
					x, y,
					p.frame[off+0], p.frame[off+1], p.frame[off+2],
					wantR, wantG, wantB)
			}
			if p.frame[off+3] != 0xFF {
				t.Fatalf("pixel ($%d, $%d) alpha = $%02X; want $FF", x, y, p.frame[off+3])
			}
		}
	}
}

// BG-show disabled: framebuffer fills with universal bg color.
func TestPPU_BGDisabledShowsUniversalColor(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// PPUMASK bit 3 stays clear → BG disabled.
	// Palette[0] = $21 (sky blue-ish).
	p.Write(0x2006, 0x3F)
	p.Write(0x2006, 0x00)
	p.Write(0x2007, 0x21)
	p.renderFrame()
	wantR, wantG, wantB := paletteRGB(0x21)
	off := (50*ScreenWidth + 50) * 4
	if p.frame[off+0] != wantR || p.frame[off+1] != wantG || p.frame[off+2] != wantB {
		t.Errorf("bg-disabled pixel = (%02X,%02X,%02X); want (%02X,%02X,%02X)",
			p.frame[off+0], p.frame[off+1], p.frame[off+2],
			wantR, wantG, wantB)
	}
}

// OAMDATA writes through $2004 land at OAM[oamAddr] and bump the
// cursor.
func TestPPU_OAMDataWriteIncrements(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2003, 0x10) // OAMADDR = $10
	p.Write(0x2004, 0xAA)
	p.Write(0x2004, 0xBB)
	if p.oam[0x10] != 0xAA || p.oam[0x11] != 0xBB {
		t.Errorf("OAM[$10..$11] = $%02X,$%02X; want $AA,$BB", p.oam[0x10], p.oam[0x11])
	}
	if p.oamAddr != 0x12 {
		t.Errorf("oamAddr = $%02X; want $12 after 2 writes", p.oamAddr)
	}
}

// Mirrored register window: writes to $2008 / $3FF8 etc. land on the
// same 8-byte register file at $2000-$2007.
func TestPPU_MirroredRegisterWindow(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2008, 0x80) // mirror of $2000
	if p.ctrl != 0x80 {
		t.Errorf("$2008 write should mirror $2000: ctrl=$%02X want $80", p.ctrl)
	}
	p.Write(0x3FFF, 0x42) // mirror of $2007
	// $2007 writes go to VRAM[v]; without setting v we just confirm it
	// didn't no-op. Easier: verify reading $3FFA returns same as $2002.
	p.status = 0xC0
	got := p.Read(0x3FFA)
	if got&0x80 == 0 {
		t.Errorf("$3FFA read should mirror $2002: got $%02X", got)
	}
}
