package nes

// Timing bundles the region-specific clock + frame-geometry constants
// that drive the CPU step budget, the PPU scanline grid, and the APU
// frame counter. NTSC is the default everywhere; PAL + Dendy carts
// (iNES 2.0 flag12 / the TVSystem field) select the alternates.
//
// The values that actually differ across regions:
//   - CPU master clock (Hz) + display refresh (fps) → cycles/frame.
//   - PPU scanlines per frame (NTSC/Dendy 262, PAL 312) + the
//     pre-render scanline index.
//   - Odd-frame dot-skip (NTSC only).
//   - APU frame-counter quarter step length (CPU cycles).
//
// Constant across regions: 341 dots/scanline, vblank flag set at
// scanline 241 dot 1, 240 visible scanlines.
type Timing struct {
	CPUClockHz         int
	FPS                int
	CyclesPerFrame     int
	DotsPerScanline    int
	ScanlinesPerFrame  int
	VBlankScanline     int
	PreRenderScanline  int
	OddFrameSkip       bool
	QuarterFrameCycles int
}

// NTSC — 2C02, 60 Hz, 1.789773 MHz. The reference everything else
// deviates from.
var NTSC = Timing{
	CPUClockHz:         1789773,
	FPS:                60,
	CyclesPerFrame:     29830, // floor(1789773/60)
	DotsPerScanline:    341,
	ScanlinesPerFrame:  262,
	VBlankScanline:     241,
	PreRenderScanline:  261,
	OddFrameSkip:       true,
	QuarterFrameCycles: 7457, // floor(1789773/240)
}

// PAL — 2C07, 50 Hz, 1.662607 MHz. 312-line frame, no odd-frame
// dot-skip, longer frame-counter step.
var PAL = Timing{
	CPUClockHz:         1662607,
	FPS:                50,
	CyclesPerFrame:     33252, // floor(1662607/50)
	DotsPerScanline:    341,
	ScanlinesPerFrame:  312,
	VBlankScanline:     241,
	PreRenderScanline:  311,
	OddFrameSkip:       false,
	QuarterFrameCycles: 8313, // floor(1662607/200)
}

// Dendy — Soviet/UMC clone, 50 Hz with the PAL CPU-ish clock but an
// NTSC-shaped 262-line PPU grid (the extra vblank lines sit in the
// post-render region). Approximated: PAL-rate CPU + NTSC PPU
// geometry, no odd-frame skip.
var Dendy = Timing{
	CPUClockHz:         1773448,
	FPS:                50,
	CyclesPerFrame:     35468, // floor(1773448/50)
	DotsPerScanline:    341,
	ScanlinesPerFrame:  262,
	VBlankScanline:     241,
	PreRenderScanline:  261,
	OddFrameSkip:       false,
	QuarterFrameCycles: 7389, // floor(1773448/240)
}

// TimingFor maps a parsed cart TV-system to its Timing. TVDual +
// any unknown value fall back to NTSC — the safe default that the
// whole existing demo + test corpus is pinned against.
func TimingFor(tv TVSystem) Timing {
	switch tv {
	case TVPAL:
		return PAL
	case TVDendy:
		return Dendy
	default:
		return NTSC
	}
}
