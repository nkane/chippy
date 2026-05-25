package nes

import "testing"

// TimingFor maps each TV-system to the right region table; unknown /
// dual fall back to NTSC.
func TestTimingFor(t *testing.T) {
	cases := []struct {
		tv   TVSystem
		want Timing
	}{
		{TVNTSC, NTSC},
		{TVPAL, PAL},
		{TVDendy, Dendy},
		{TVDual, NTSC},       // dual → NTSC fallback
		{TVSystem(99), NTSC}, // unknown → NTSC fallback
	}
	for _, c := range cases {
		if got := TimingFor(c.tv); got != c.want {
			t.Errorf("TimingFor(%v) = %+v; want %+v", c.tv, got, c.want)
		}
	}
}

// Region geometry sanity: PAL has more scanlines + no dot-skip, NTSC
// + Dendy run 262 lines, NTSC alone does the odd-frame skip.
func TestTimingGeometry(t *testing.T) {
	if NTSC.ScanlinesPerFrame != 262 || PAL.ScanlinesPerFrame != 312 || Dendy.ScanlinesPerFrame != 262 {
		t.Errorf("scanline counts wrong: NTSC=%d PAL=%d Dendy=%d",
			NTSC.ScanlinesPerFrame, PAL.ScanlinesPerFrame, Dendy.ScanlinesPerFrame)
	}
	if !NTSC.OddFrameSkip {
		t.Errorf("NTSC must do odd-frame dot-skip")
	}
	if PAL.OddFrameSkip || Dendy.OddFrameSkip {
		t.Errorf("PAL/Dendy must not do odd-frame dot-skip")
	}
	// All regions share 341 dots + vblank at scanline 241.
	for _, tm := range []Timing{NTSC, PAL, Dendy} {
		if tm.DotsPerScanline != 341 {
			t.Errorf("dots/scanline = %d; want 341", tm.DotsPerScanline)
		}
		if tm.VBlankScanline != 241 {
			t.Errorf("vblank scanline = %d; want 241", tm.VBlankScanline)
		}
		if tm.PreRenderScanline != tm.ScanlinesPerFrame-1 {
			t.Errorf("pre-render %d != last scanline %d", tm.PreRenderScanline, tm.ScanlinesPerFrame-1)
		}
	}
	// PAL runs slower (lower clock) but at 50 Hz.
	if PAL.FPS != 50 || Dendy.FPS != 50 || NTSC.FPS != 60 {
		t.Errorf("fps wrong: NTSC=%d PAL=%d Dendy=%d", NTSC.FPS, PAL.FPS, Dendy.FPS)
	}
}
