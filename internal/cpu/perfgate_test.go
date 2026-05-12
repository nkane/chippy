//go:build perfgate

package cpu

import (
	"encoding/json"
	"os"
	"testing"
)

// TestPerfGate is the CI safety net for issue #113. Runs each
// benchmark programmatically, compares ns/op against the checked-in
// baseline, and fails when any number exceeds the documented
// `ns_op_max` by more than 15%.
//
// Behind the `perfgate` build tag so a normal `go test` doesn't burn
// CPU on it. CI runs:
//
//	go test -tags=perfgate -timeout 5m -run TestPerfGate ./internal/cpu/...
//
// Refresh the baseline on a known-good ubuntu-latest commit (see
// docs/perf-baseline.md for the recipe).
func TestPerfGate(t *testing.T) {
	const tolerance = 1.15 // 15% slower than baseline ns/op_max fails.

	type entry struct {
		NsOpMax float64 `json:"ns_op_max"`
	}
	type baseline = map[string]entry
	var base baseline

	raw, err := os.ReadFile("testdata/perf-baseline.json")
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	// Tolerate the "_comment" string field in the JSON by unmarshaling
	// into a flexible map first and skipping non-object values.
	var asAny map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asAny); err != nil {
		t.Fatalf("parse baseline: %v", err)
	}
	base = baseline{}
	for k, v := range asAny {
		var e entry
		if err := json.Unmarshal(v, &e); err == nil && e.NsOpMax > 0 {
			base[k] = e
		}
	}

	cases := []struct {
		name string
		fn   func(*testing.B)
	}{
		{"BenchmarkStep_NMOS", BenchmarkStep_NMOS},
		{"BenchmarkStep_CMOS", BenchmarkStep_CMOS},
		{"BenchmarkStep_WithSnapshot", BenchmarkStep_WithSnapshot},
	}
	for _, c := range cases {
		want, ok := base[c.name]
		if !ok {
			t.Errorf("%s: no baseline entry", c.name)
			continue
		}
		// 1 second per benchmark keeps the gate runtime predictable
		// while still averaging out noise on a shared CI runner.
		r := testing.Benchmark(c.fn)
		got := float64(r.NsPerOp())
		if got > want.NsOpMax*tolerance {
			t.Errorf("%s: %0.2f ns/op exceeds baseline %0.2f * %0.2f tolerance = %0.2f",
				c.name, got, want.NsOpMax, tolerance, want.NsOpMax*tolerance)
			continue
		}
		t.Logf("%s: %0.2f ns/op (baseline ceiling %0.2f, headroom %0.0f%%)",
			c.name, got, want.NsOpMax, (want.NsOpMax-got)/want.NsOpMax*100)
	}
}
