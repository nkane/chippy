package main

import "testing"

// sunsoft5b-chord programs all three Sunsoft 5B tone channels at
// distinct periods + fixed-level amplitudes. Asserts:
//  1. cart dispatches to FME-7 + the 5B audio sink wires up.
//  2. APU emits non-zero samples (the 5B chip is being stepped +
//     its output reaches the mixer).
func TestDemo_Sunsoft5BChord_EmitsAudio(t *testing.T) {
	_, bus := runDemoFramesWithBus(t, "../../roms/demos/sunsoft5b-chord/sunsoft5b-chord.nes", 30)
	if bus.apu.Sunsoft5B() == nil {
		t.Fatalf("APU has no Sunsoft 5B audio chip wired")
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
		t.Errorf("Sunsoft 5B chord produced silence")
	}
}
