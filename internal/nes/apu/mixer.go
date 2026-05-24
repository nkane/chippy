// Non-linear DAC mixer per nesdev wiki. Replaces v0.2's linear
// `pulse1 + pulse2 + (...)` approximation. The formulas are:
//
//	pulse_out = 95.88 / ((8128 / (p1 + p2)) + 100)              when p1+p2 > 0
//	tnd_out   = 159.79 / ((1 / (t/8227 + n/12241 + d/22638)) + 100)
//	output    = pulse_out + tnd_out                              float in [0, ~1.0]
//
// The pulse term is a function of p1+p2 only (31 distinct values),
// so we precompute it once into pulseTable. The tnd term has too
// many inputs (16 * 16 * 128 = 32768 entries) to make a full LUT
// worthwhile — we evaluate it inline; the per-sample division is
// trivial vs the rest of the per-sample cost.

package apu

var pulseTable [31]float32

func init() {
	pulseTable[0] = 0
	for i := 1; i < len(pulseTable); i++ {
		pulseTable[i] = 95.88 / ((8128.0 / float32(i)) + 100)
	}
}

// mixSample combines the five channel outputs through the non-
// linear DAC mixer and returns a float in roughly [0, 1.0]. The
// caller scales to int16 / int32 / float32 as needed.
func mixSample(p1, p2, tri, noi, dmc byte) float32 {
	pulse := pulseTable[p1+p2]
	var tnd float32
	if tri != 0 || noi != 0 || dmc != 0 {
		denom := float32(tri)/8227.0 +
			float32(noi)/12241.0 +
			float32(dmc)/22638.0
		tnd = 159.79 / ((1.0 / denom) + 100.0)
	}
	return pulse + tnd
}
