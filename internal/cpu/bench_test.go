package cpu

import "testing"

// benchSetup primes a CPU with a 64 KiB sea of NOPs so Step() marches
// through addresses without hitting a self-jump halt. PC wraps from
// $FFFF → $0000 naturally.
func benchSetup(variant Variant) (*CPU, *RAM) {
	ram := NewRAM()
	for i := range ram.Data {
		ram.Data[i] = 0xEA // NOP
	}
	ram.Write(VecReset, 0x00)
	ram.Write(VecReset+1, 0x00)
	c := NewVariant(ram, variant)
	return c, ram
}

func BenchmarkStep_NMOS(b *testing.B) {
	c, _ := benchSetup(VariantNMOS)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Step()
	}
}

func BenchmarkStep_CMOS(b *testing.B) {
	c, _ := benchSetup(VariantCMOS65C02)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Step()
	}
}

// BenchmarkStep_WithSnapshot models the cost of CoW page tracking under
// active reverse-step recording (issue #66). Each iteration enables the
// shadow, runs a Step, and claims the delta — what the TUI's tickMsg
// loop and DAP runLoop pay per step when the rewind ring is on.
func BenchmarkStep_WithSnapshot(b *testing.B) {
	c, ram := benchSetup(VariantNMOS)
	ram.EnableShadow()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ram.ResetShadow()
		c.Step()
		_ = ram.TakeShadow()
	}
}
