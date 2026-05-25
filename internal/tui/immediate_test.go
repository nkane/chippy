package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

func newImmediateModel() Model {
	c := cpu.New(cpu.NewRAM())
	return New(c, cpu.NewRAM())
}

func TestImmediate_EvaluateBasic(t *testing.T) {
	m := newImmediateModel()
	m.CPU.A = 0x11
	m.CPU.X = 0x22
	got := m.evaluateImmediate("A + X")
	if got.Err {
		t.Fatalf("eval error: %s", got.Result)
	}
	if got.Result != "$33  (51)" {
		t.Fatalf("want $33 (51), got %q", got.Result)
	}
}

func TestImmediate_MemoryDeref(t *testing.T) {
	m := newImmediateModel()
	m.RAM.Write(0x0200, 0xAB)
	got := m.evaluateImmediate("[$0200]")
	if got.Err {
		t.Fatalf("eval error: %s", got.Result)
	}
	if got.Result != "$AB  (171)" {
		t.Fatalf("want $AB (171), got %q", got.Result)
	}
}

func TestImmediate_BadExpression(t *testing.T) {
	m := newImmediateModel()
	got := m.evaluateImmediate("A +")
	if !got.Err {
		t.Fatalf("malformed expression should report error")
	}
}

// `-1` in the immediate window should render as `$FF (255)` so a
// register-byte comparison feels natural. Pins the width-aware unary
// minus from internal/expr (issue #129).
func TestImmediate_UnaryMinusRendersAsByte(t *testing.T) {
	m := newImmediateModel()
	got := m.evaluateImmediate("-1")
	if got.Err {
		t.Fatalf("eval error: %s", got.Result)
	}
	if got.Result != "$FF  (255)" {
		t.Fatalf("want $FF (255), got %q", got.Result)
	}
}

func TestImmediate_RegisterEqualsNegativeOne(t *testing.T) {
	m := newImmediateModel()
	m.CPU.A = 0xFF
	got := m.evaluateImmediate("A == -1")
	if got.Err {
		t.Fatalf("eval error: %s", got.Result)
	}
	if got.Result != "$01  (1)" {
		t.Fatalf("A($FF) == -1 should be true ($01); got %q", got.Result)
	}
}

func TestImmediate_FormatWidthByMagnitude(t *testing.T) {
	cases := []struct {
		v    uint32
		want string
	}{
		{0x42, "$42  (66)"},
		{0xFF, "$FF  (255)"},
		{0x100, "$0100  (256)"},
		{0xFFFF, "$FFFF  (65535)"},
		{0x10000, "$00010000  (65536)"},
	}
	for _, c := range cases {
		if got := formatImmediateResult(c.v); got != c.want {
			t.Errorf("formatImmediateResult($%X): want %q, got %q", c.v, c.want, got)
		}
	}
}
