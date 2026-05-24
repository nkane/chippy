package apu

import (
	"math"
	"testing"
)

// All channels silent → mixer outputs exactly 0. Catches a
// regression where the pulse / tnd formulas leak a non-zero base.
func TestMixer_SilentInputsZero(t *testing.T) {
	if got := mixSample(0, 0, 0, 0, 0); got != 0 {
		t.Errorf("mixSample(0,0,0,0,0) = %v; want 0", got)
	}
}

// Pulse table value at i=0 is 0; values monotonically increase.
// Peak (p1+p2 = 30) sits around 0.258 per nesdev's published
// formula. Allow ±5% slack for float32 rounding.
func TestMixer_PulseTableShape(t *testing.T) {
	if pulseTable[0] != 0 {
		t.Errorf("pulseTable[0] = %v; want 0", pulseTable[0])
	}
	for i := 1; i < len(pulseTable); i++ {
		if pulseTable[i] <= pulseTable[i-1] {
			t.Errorf("pulseTable[%d]=%v not > pulseTable[%d]=%v",
				i, pulseTable[i], i-1, pulseTable[i-1])
		}
	}
	peak := pulseTable[30]
	if math.Abs(float64(peak-0.258)) > 0.02 {
		t.Errorf("pulseTable[30] = %v; want ~0.258", peak)
	}
}

// tnd term contribution: a triangle-only mix has different
// magnitude than a pulse-only mix at "equivalent" volume (the
// non-linearity is the whole point). Compare pulse=15 vs triangle=15
// — both at half-volume of their respective max — and assert they
// produce different output levels.
func TestMixer_TriangleVsPulseDistinctLevels(t *testing.T) {
	pulseOnly := mixSample(15, 0, 0, 0, 0)
	triOnly := mixSample(0, 0, 15, 0, 0)
	if pulseOnly == triOnly {
		t.Errorf("pulse-15 + tri-15 mix to identical levels; non-linear DAC should differ")
	}
	// Both must be positive.
	if pulseOnly <= 0 || triOnly <= 0 {
		t.Errorf("got non-positive levels: pulse=%v tri=%v", pulseOnly, triOnly)
	}
}

// Combined channels produce strictly greater output than any single
// channel alone — additive within each group, with the non-linear
// pulse + tnd sum staying monotone.
func TestMixer_CombinedExceedsSingle(t *testing.T) {
	pulseOnly := mixSample(15, 15, 0, 0, 0)
	triOnly := mixSample(0, 0, 15, 0, 0)
	all := mixSample(15, 15, 15, 8, 64)
	if all <= pulseOnly {
		t.Errorf("all-channels (%v) should exceed pulse-only (%v)", all, pulseOnly)
	}
	if all <= triOnly {
		t.Errorf("all-channels (%v) should exceed triangle-only (%v)", all, triOnly)
	}
}

// Output stays within int16 headroom after scaling. With the
// chosen scale factor (30000) and all channels at peak, the
// mixer output must not clip int16.
func TestMixer_NoInt16Clipping(t *testing.T) {
	mix := mixSample(15, 15, 15, 15, 127) // every channel max
	scaled := mix * 30000
	if scaled > 32767 || scaled < -32768 {
		t.Errorf("max-mix scaled = %v; would clip int16", scaled)
	}
}
