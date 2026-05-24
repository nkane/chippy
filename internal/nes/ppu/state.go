package ppu

import "errors"

// FullState is the gob-serializable full-PPU capture for nessy save
// states (#266). Captures every field that influences the next dot's
// behaviour: register latches, VRAM / OAM / palette, timing
// (scanline / dot / frameCount), scroll-snapshot history, both
// framebuffers, and the BG-opaque mask used by the sprite
// compositor.
//
// `cart` and `nmi` aren't part of state — they're re-bound from the
// post-restore PPU's wiring.
type FullState struct {
	Ctrl, Mask, Status, OAMAddr byte
	V, T                        uint16
	X                           byte
	W                           bool
	ReadBuf                     byte
	ScrollX, ScrollY            byte
	ScrollHi                    bool
	OpenBus                     byte

	VRAM    [0x800]byte
	OAM     [256]byte
	Palette [32]byte

	Scanline   int
	Dot        int
	FrameCount uint64

	Frame        []byte // ScreenWidth*ScreenHeight*4 length
	DisplayFrame []byte
	BGOpaque     []bool

	FrameStartScroll ScrollSnapshotState
	ScrollEvents     []ScrollSnapshotState
}

// ScrollSnapshotState mirrors the package-private scrollSnapshot
// with exported fields gob can encode.
type ScrollSnapshotState struct {
	Scanline      int
	ScrollX       byte
	ScrollY       byte
	BaseNametable byte
}

// SaveFullState copies the PPU's mutable state into a FullState.
// Cheap — 2 KiB VRAM + 256 B OAM + 32 B palette + 2 × 245 KiB
// framebuffer + 61 KiB bgOpaque ≈ 550 KiB per save.
func (p *PPU) SaveFullState() FullState {
	p.displayMu.Lock()
	defer p.displayMu.Unlock()

	st := FullState{
		Ctrl: p.ctrl, Mask: p.mask, Status: p.status, OAMAddr: p.oamAddr,
		V: p.v, T: p.t, X: p.x, W: p.w, ReadBuf: p.readBuf,
		ScrollX: p.scrollX, ScrollY: p.scrollY, ScrollHi: p.scrollHi,
		OpenBus: p.openBus,
		VRAM: p.vram, OAM: p.oam, Palette: p.palette,
		Scanline: p.scanline, Dot: p.dot, FrameCount: p.frameCount,
		FrameStartScroll: exportScroll(p.frameStartScroll),
	}
	st.Frame = make([]byte, len(p.frame))
	copy(st.Frame, p.frame[:])
	st.DisplayFrame = make([]byte, len(p.displayFrame))
	copy(st.DisplayFrame, p.displayFrame[:])
	st.BGOpaque = make([]bool, len(p.bgOpaque))
	copy(st.BGOpaque, p.bgOpaque[:])
	if len(p.scrollEvents) > 0 {
		st.ScrollEvents = make([]ScrollSnapshotState, len(p.scrollEvents))
		for i, e := range p.scrollEvents {
			st.ScrollEvents[i] = exportScroll(e)
		}
	}
	return st
}

// LoadFullState overwrites the PPU's state from s. Lengths must match
// the framebuffer / bgOpaque sizes — mismatched input is rejected so
// a malformed save can't half-write video memory.
func (p *PPU) LoadFullState(s FullState) error {
	fbSize := ScreenWidth * ScreenHeight * 4
	maskSize := ScreenWidth * ScreenHeight
	if len(s.Frame) != fbSize || len(s.DisplayFrame) != fbSize {
		return errBadStateSize
	}
	if len(s.BGOpaque) != maskSize {
		return errBadStateSize
	}

	p.displayMu.Lock()
	defer p.displayMu.Unlock()

	p.ctrl, p.mask, p.status, p.oamAddr = s.Ctrl, s.Mask, s.Status, s.OAMAddr
	p.v, p.t, p.x, p.w, p.readBuf = s.V, s.T, s.X, s.W, s.ReadBuf
	p.scrollX, p.scrollY, p.scrollHi = s.ScrollX, s.ScrollY, s.ScrollHi
	p.openBus = s.OpenBus
	p.vram = s.VRAM
	p.oam = s.OAM
	p.palette = s.Palette
	p.scanline, p.dot, p.frameCount = s.Scanline, s.Dot, s.FrameCount
	p.frameStartScroll = importScroll(s.FrameStartScroll)
	copy(p.frame[:], s.Frame)
	copy(p.displayFrame[:], s.DisplayFrame)
	copy(p.bgOpaque[:], s.BGOpaque)
	p.scrollEvents = p.scrollEvents[:0]
	for _, e := range s.ScrollEvents {
		p.scrollEvents = append(p.scrollEvents, importScroll(e))
	}
	return nil
}

func exportScroll(s scrollSnapshot) ScrollSnapshotState {
	return ScrollSnapshotState{Scanline: s.scanline, ScrollX: s.scrollX, ScrollY: s.scrollY, BaseNametable: s.baseNametable}
}

func importScroll(s ScrollSnapshotState) scrollSnapshot {
	return scrollSnapshot{scanline: s.Scanline, scrollX: s.ScrollX, scrollY: s.ScrollY, baseNametable: s.BaseNametable}
}

var errBadStateSize = errors.New("save-state payload size mismatch")
