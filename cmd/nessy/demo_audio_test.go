package main

import (
	"testing"
)

// audio-test ROM cycles a 12-note chromatic scale on pulse 1.
// Headless verification: boot the ROM, run a few frames, confirm
// the APU emitted non-zero samples (the chromatic-scale program
// configured pulse 1 with constant volume 7 and a held length
// counter, so the channel should be audible from boot).
//
// We don't pin a sample-buffer SHA the way the visual demos pin a
// framebuffer hash — sample timing is sensitive to cycle-per-sample
// fractional accumulator drift, so a SHA would be brittle. Instead
// we just assert non-silence + a sane sample count.
func TestDemo_AudioTest_EmitsSamples(t *testing.T) {
	// Run ~30 frames (~0.5 s) so the reset path completes both
	// vblank-warmup waits and the program loop reaches `forever`.
	_, bus := runDemoFramesWithBus(t, "../../roms/demos/audio-test/audio-test.nes", 30)
	samples := bus.apu.Samples()
	if len(samples) == 0 {
		t.Fatalf("APU emitted zero samples; audio-test pulse never ran")
	}
	// The APU ring caps at SampleRate/4 (~250 ms of audio) so a long
	// run without intermediate Samples() drains saturates the
	// buffer. 30 frames produce ~22 k samples; the ring trims to
	// ~11 k. Assert we're at the high water mark — fewer would mean
	// the channel went silent before the run finished.
	if len(samples) < 10_000 {
		t.Errorf("sample count = %d; want >= 10000 (ring should be near full)", len(samples))
	}
	// Confirm non-silence — at least one sample is non-zero. Pulse 1
	// at volume 7, 50% duty should produce alternating 0 / non-zero
	// runs.
	nonZero := 0
	for _, s := range samples {
		if s != 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Errorf("all %d samples are zero; pulse 1 didn't sound", len(samples))
	}
	// Sanity-check the rough duty cycle. 50% duty + non-silenced =
	// roughly half the samples should be non-zero (allow wide
	// tolerance for transition fringes).
	frac := float64(nonZero) / float64(len(samples))
	if frac < 0.25 || frac > 0.75 {
		t.Errorf("non-zero fraction = %.3f; want ~0.5 for 50%% duty", frac)
	}
}
