package main

import (
	"testing"
)

// vrc6-chord programs all three VRC6 audio channels (pulse 1 +
// pulse 2 + sawtooth) at distinct frequencies and spins. Asserts:
//  1. cart dispatches to VRC6 + the audio sink wires up.
//  2. APU emits non-zero samples (the VRC6 chip is being stepped
//     + its output reaches the mixer).
func TestDemo_VRC6Chord_EmitsAudio(t *testing.T) {
	_, bus := runDemoFramesWithBus(t, "../../roms/demos/vrc6-chord/vrc6-chord.nes", 30)
	if bus.apu.VRC6Audio() == nil {
		t.Fatalf("APU has no VRC6 audio chip wired")
	}
	samples := bus.apu.Samples()
	if len(samples) < 10_000 {
		t.Errorf("sample count = %d; want ring near saturation", len(samples))
	}
	nonZero := false
	for _, s := range samples {
		if s != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Errorf("VRC6 chord produced silence — chip wired but not stepping")
	}
}
