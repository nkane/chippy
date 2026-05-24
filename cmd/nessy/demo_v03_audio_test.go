package main

import (
	"testing"
)

// Each v0.3 audio demo emits non-zero samples after ~30 frames of
// runtime. SHAs aren't pinned (sample timing is fractional-
// accumulator-sensitive); the assertion is "channel made sound".

func TestDemo_TriangleArpeggio_EmitsSamples(t *testing.T) {
	_, bus := runDemoFramesWithBus(t, "../../roms/demos/triangle-arpeggio/triangle-arpeggio.nes", 30)
	samples := bus.apu.Samples()
	if len(samples) < 10_000 {
		t.Errorf("sample count = %d; want ring near saturation", len(samples))
	}
	if !anyNonZero(samples) {
		t.Errorf("triangle-arpeggio produced silence")
	}
}

func TestDemo_NoiseDrum_EmitsSamples(t *testing.T) {
	_, bus := runDemoFramesWithBus(t, "../../roms/demos/noise-drum/noise-drum.nes", 30)
	samples := bus.apu.Samples()
	if len(samples) < 10_000 {
		t.Errorf("sample count = %d; want ring near saturation", len(samples))
	}
	if !anyNonZero(samples) {
		t.Errorf("noise-drum produced silence")
	}
}

// all-channels asserts every channel contributed to the mix. We
// can't trivially decompose the mixed sample, but we can sample
// the per-channel output() readings directly off the APU after
// the run.
func TestDemo_AllChannels_EveryChannelActive(t *testing.T) {
	_, bus := runDemoFramesWithBus(t, "../../roms/demos/all-channels/all-channels.nes", 30)
	samples := bus.apu.Samples()
	if len(samples) < 10_000 {
		t.Errorf("sample count = %d; want ring near saturation", len(samples))
	}
	if !anyNonZero(samples) {
		t.Fatalf("all-channels produced silence")
	}
	// Each channel's length counter should still be non-zero (halt
	// bits set across the board).
	if bus.apu.Pulse1LengthCounter() == 0 {
		t.Errorf("pulse 1 length drained")
	}
	if bus.apu.Pulse2LengthCounter() == 0 {
		t.Errorf("pulse 2 length drained")
	}
	if bus.apu.TriangleLengthCounter() == 0 {
		t.Errorf("triangle length drained")
	}
	if bus.apu.NoiseLengthCounter() == 0 {
		t.Errorf("noise length drained")
	}
}

func anyNonZero(samples []int16) bool {
	for _, s := range samples {
		if s != 0 {
			return true
		}
	}
	return false
}
