package apu

// lengthCounterLUT maps the 5-bit value from $4003 / $4007 (bits 3-7)
// to the actual length-counter load. Per nesdev wiki — tested
// against real silicon. Channel goes silent when this counter
// reaches zero (gated by !$4017 halt bit).
var lengthCounterLUT = [32]byte{
	10, 254, 20, 2, 40, 4, 80, 6, 160, 8, 60, 10, 14, 12, 26, 14,
	12, 16, 24, 18, 48, 20, 96, 22, 192, 24, 72, 26, 16, 28, 32, 30,
}

// dutyWaveforms is the four duty-cycle bit patterns for the pulse
// channels, picked by $4000 / $4004 bits 6-7. The position-7 bit
// emits first; index advances clockwise per period elapse.
//
//	0: 12.5%  → 0 1 0 0 0 0 0 0
//	1: 25%    → 0 1 1 0 0 0 0 0
//	2: 50%    → 0 1 1 1 1 0 0 0
//	3: 25% neg→ 1 0 0 1 1 1 1 1
var dutyWaveforms = [4][8]byte{
	{0, 1, 0, 0, 0, 0, 0, 0},
	{0, 1, 1, 0, 0, 0, 0, 0},
	{0, 1, 1, 1, 1, 0, 0, 0},
	{1, 0, 0, 1, 1, 1, 1, 1},
}
