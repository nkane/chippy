package cpu

import "testing"

// AssertIRQSource raises the line; multiple sources OR together so a
// partial release leaves the line asserted.
func TestIRQSources_MultiSourceOR(t *testing.T) {
	c := New(NewRAM())
	if c.IRQAsserted() {
		t.Fatalf("pre-test IRQ asserted")
	}
	c.AssertIRQSource("frame")
	c.AssertIRQSource("dmc")
	if !c.IRQAsserted() {
		t.Errorf("both sources asserted; line should be high")
	}
	// Clear one — line still high.
	c.ClearIRQSource("frame")
	if !c.IRQAsserted() {
		t.Errorf("clearing one of two sources should leave line high")
	}
	// Clear the second — line goes low.
	c.ClearIRQSource("dmc")
	if c.IRQAsserted() {
		t.Errorf("clearing last source should lower line")
	}
}

// Asserting the same source twice + clearing once should fully
// release it (set semantics, not refcount).
func TestIRQSources_IdempotentAssertion(t *testing.T) {
	c := New(NewRAM())
	c.AssertIRQSource("frame")
	c.AssertIRQSource("frame")
	c.ClearIRQSource("frame")
	if c.IRQAsserted() {
		t.Errorf("set-based assert: clearing once should suffice")
	}
}

// AssertIRQ() / ReleaseIRQ() are the no-source convenience wrappers
// — they map to source "". They must compose cleanly with named
// sources without trampling them.
func TestIRQSources_AnonymousSourceCoexistsWithNamed(t *testing.T) {
	c := New(NewRAM())
	c.AssertIRQ() // anonymous source
	c.AssertIRQSource("dmc")
	if !c.IRQAsserted() {
		t.Fatalf("both anon + dmc asserted; line should be high")
	}
	c.ReleaseIRQ() // clears only the anonymous source
	if !c.IRQAsserted() {
		t.Errorf("ReleaseIRQ cleared a named source; should only touch anon")
	}
	c.ClearIRQSource("dmc")
	if c.IRQAsserted() {
		t.Errorf("after clearing both, line should be low")
	}
}

// Clearing a source that was never asserted is a no-op (must not
// panic, must not change line state).
func TestIRQSources_ClearUnknownIsNoop(t *testing.T) {
	c := New(NewRAM())
	c.ClearIRQSource("never-asserted")
	if c.IRQAsserted() {
		t.Errorf("phantom source asserted line")
	}
	c.AssertIRQSource("real")
	c.ClearIRQSource("ghost")
	if !c.IRQAsserted() {
		t.Errorf("clearing unknown source dropped a real assertion")
	}
}
