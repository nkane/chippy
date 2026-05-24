package apu

import "testing"

// fakeSink records every Assert / Clear call so tests can assert
// the named-source IRQ surface without spinning up a real CPU.
type fakeSink struct {
	asserted map[string]int // count of Assert per source
	cleared  map[string]int // count of Clear per source
}

func newFakeSink() *fakeSink {
	return &fakeSink{
		asserted: map[string]int{},
		cleared:  map[string]int{},
	}
}

func (s *fakeSink) AssertIRQSource(src string) { s.asserted[src]++ }
func (s *fakeSink) ClearIRQSource(src string)  { s.cleared[src]++ }

// In 4-step mode the frame counter fires the IRQ exactly once per
// 4-step cycle (29830 CPU cycles, ~16.7 ms) when not inhibited.
// After ~5 cycles' worth we expect ~5 asserts + the flag latched.
func TestAPU_FrameIRQ_FiresIn4StepMode(t *testing.T) {
	a := New()
	sink := newFakeSink()
	a.SetIRQSink(sink)
	// $4017 = 0 → 4-step, IRQ not inhibited.
	a.SetFrameCounter(0x00)

	// One full 4-step frame is 4 quarter-frames = 4 * 7457 = 29828
	// CPU cycles. Run for 5 frames so the assertion is unambiguous.
	a.Tick(5 * 29828)
	if got := sink.asserted[frameIRQSource]; got < 4 {
		t.Errorf("frame IRQ assert count = %d; want >= 4 over 5 frames", got)
	}
	if !a.frameIRQFlag {
		t.Errorf("frameIRQFlag should be latched after firing")
	}
}

// $4017 with bit 6 set inhibits the IRQ + clears any pending flag
// immediately.
func TestAPU_FrameIRQ_InhibitClearsPendingAndPrevents(t *testing.T) {
	a := New()
	sink := newFakeSink()
	a.SetIRQSink(sink)
	a.SetFrameCounter(0x00) // not inhibited
	a.Tick(5 * 29828)       // accumulate pending IRQ
	if !a.frameIRQFlag {
		t.Fatalf("pre-condition: expected pending frame IRQ")
	}
	// Now flip inhibit on — flag clears, sink sees Clear.
	a.SetFrameCounter(0x40)
	if a.frameIRQFlag {
		t.Errorf("inhibit didn't clear pending flag")
	}
	if sink.cleared[frameIRQSource] == 0 {
		t.Errorf("inhibit didn't call ClearIRQSource")
	}
	// Run another 5 frames — no new asserts.
	preAssert := sink.asserted[frameIRQSource]
	a.Tick(5 * 29828)
	if sink.asserted[frameIRQSource] != preAssert {
		t.Errorf("inhibited mode still fired %d new IRQ(s)", sink.asserted[frameIRQSource]-preAssert)
	}
}

// 5-step mode never fires the frame-counter IRQ regardless of the
// inhibit bit.
func TestAPU_FrameIRQ_5StepModeNeverFires(t *testing.T) {
	a := New()
	sink := newFakeSink()
	a.SetIRQSink(sink)
	// $4017 = $80 → 5-step, IRQ not "inhibited" (bit 6 = 0) but
	// 5-step skips IRQ entirely.
	a.SetFrameCounter(0x80)
	a.Tick(10 * 29828)
	if got := sink.asserted[frameIRQSource]; got != 0 {
		t.Errorf("5-step asserted IRQ %d times; want 0", got)
	}
	if a.frameIRQFlag {
		t.Errorf("5-step latched frameIRQFlag")
	}
}

// Reading $4015 returns bit 6 = frame IRQ flag and clears it (per
// nesdev). The flag-and-source are both reset.
func TestAPU_Read4015_ClearsFrameIRQ(t *testing.T) {
	a := New()
	sink := newFakeSink()
	a.SetIRQSink(sink)
	a.SetFrameCounter(0x00)
	a.Tick(5 * 29828)
	if !a.frameIRQFlag {
		t.Fatalf("pre-condition: frame IRQ pending")
	}
	v := a.Read(0x4015)
	if v&0x40 == 0 {
		t.Errorf("$4015 read = $%02X; want bit 6 set", v)
	}
	if a.frameIRQFlag {
		t.Errorf("$4015 read didn't clear frame IRQ flag")
	}
	if sink.cleared[frameIRQSource] == 0 {
		t.Errorf("$4015 read didn't call ClearIRQSource")
	}
}

// Headless APU (no sink wired) still tracks the IRQ flag so the
// $4015 read API works in tests that don't bother attaching a
// CPU.
func TestAPU_FrameIRQ_NoSinkStillTracksFlag(t *testing.T) {
	a := New()
	// Deliberately no SetIRQSink call.
	a.SetFrameCounter(0x00)
	a.Tick(5 * 29828)
	if !a.frameIRQFlag {
		t.Errorf("flag should latch even without an IRQSink")
	}
	if got := a.Read(0x4015); got&0x40 == 0 {
		t.Errorf("$4015 read should report bit 6")
	}
	if a.frameIRQFlag {
		t.Errorf("post-read flag should clear")
	}
}
