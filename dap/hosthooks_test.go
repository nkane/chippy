package dap

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nkane/chippy/cpu"
)

func hostHookSession(t *testing.T) (*Server, *InprocClient, *cpu.CPU) {
	t.Helper()
	ram := cpu.NewRAM()
	for a := 0x8000; a < 0x9000; a++ {
		ram.Write(uint16(a), 0xEA) // NOP sled
	}
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()
	srv, cl := NewInprocServer()
	if err := srv.AttachExisting(AttachConfig{CPU: c, RAM: ram}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	return srv, cl, c
}

// SetStopPredicate stops the continue loop once the host condition trips.
func TestHostStopPredicate(t *testing.T) {
	srv, cl, _ := hostHookSession(t)
	var n atomic.Int32
	srv.SetStopPredicate(func() bool {
		return n.Add(1) >= 5
	})
	_, _ = cl.Initialize()
	_, _ = cl.Attach() // emits a stopped("entry") we ignore below
	if _, err := cl.Request("continue", map[string]any{"threadId": 1}); err != nil {
		t.Fatalf("continue: %v", err)
	}
	if !waitStopReason(t, cl, "step") {
		t.Fatal("predicate never stopped the run with reason=step")
	}
	if got := n.Load(); got != 5 {
		t.Errorf("predicate ran %d times; want 5", got)
	}
}

// waitStopReason drains events until a `stopped` with the given reason, or
// times out. Skips the attach `entry` stop and chippy-state noise.
func waitStopReason(t *testing.T, cl *InprocClient, reason string) bool {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-cl.Events():
			if ev.Event != "stopped" {
				continue
			}
			raw, _ := json.Marshal(ev.Body)
			var b struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(raw, &b)
			if b.Reason == reason {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// SetHostVars exposes a host identifier to `evaluate`.
func TestHostVarsEvaluate(t *testing.T) {
	srv, cl, _ := hostHookSession(t)
	srv.SetHostVars(func(name string) (func() uint32, bool) {
		if name == "scanline" {
			return func() uint32 { return 42 }, true
		}
		return nil, false
	})
	_, _ = cl.Initialize()
	_, _ = cl.Attach()
	resp, err := cl.Request("evaluate", EvaluateArguments{Expression: "scanline + 1"})
	if err != nil || !resp.Success {
		t.Fatalf("evaluate: %v / %+v", err, resp)
	}
	var b struct {
		Result string `json:"result"`
	}
	raw, _ := json.Marshal(resp.Body)
	_ = json.Unmarshal(raw, &b)
	if want := formatEvalResult(43); b.Result != want {
		t.Errorf("evaluate scanline+1 = %q; want %q", b.Result, want)
	}
}
