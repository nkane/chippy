package dap

import (
	"testing"
	"time"

	"github.com/nkane/chippy/cpu"
)

// TestChippyStateStream verifies the custom live-state event fires during a
// free-run and carries advancing CPU state. Uses the inproc transport, where
// the event body arrives as a ChippyStateBody struct (no unmarshal).
func TestChippyStateStream(t *testing.T) {
	ram := cpu.NewRAM()
	// Tight infinite loop: $8000 NOP; $8001 JMP $8000.
	ram.Write(0x8000, 0xEA)
	ram.Write(0x8001, 0x4C)
	ram.Write(0x8002, 0x00)
	ram.Write(0x8003, 0x80)
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()

	srv, cl := NewInprocServer()
	if err := srv.AttachExisting(AttachConfig{CPU: c, RAM: ram}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, err := cl.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Attach(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Request("continue", map[string]any{"threadId": 1}); err != nil {
		t.Fatalf("continue: %v", err)
	}

	var states []ChippyStateBody
	deadline := time.After(2 * time.Second)
collect:
	for len(states) < 3 {
		select {
		case ev := <-cl.Events():
			if ev.Event != ChippyStateEvent {
				continue
			}
			cs, ok := ev.Body.(ChippyStateBody)
			if !ok {
				t.Fatalf("chippy-state body type = %T; want ChippyStateBody", ev.Body)
			}
			states = append(states, cs)
		case <-deadline:
			break collect
		}
	}
	_, _ = cl.Request("pause", map[string]any{"threadId": 1})

	if len(states) < 2 {
		t.Fatalf("got %d chippy-state events; want >= 2", len(states))
	}
	// Cycles strictly increase across throttled samples.
	for i := 1; i < len(states); i++ {
		if states[i].Cycles <= states[i-1].Cycles {
			t.Errorf("cycles not advancing: %d then %d", states[i-1].Cycles, states[i].Cycles)
		}
	}
}

// TestChippyStateThrottle checks the event rate stays near the 60 Hz cap
// rather than firing per-instruction.
func TestChippyStateThrottle(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0xEA)
	ram.Write(0x8001, 0x4C)
	ram.Write(0x8002, 0x00)
	ram.Write(0x8003, 0x80)
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()
	srv, cl := NewInprocServer()
	_ = srv.AttachExisting(AttachConfig{CPU: c, RAM: ram})
	_, _ = cl.Initialize()
	_, _ = cl.Attach()
	_, _ = cl.Request("continue", map[string]any{"threadId": 1})

	const window = 250 * time.Millisecond
	n := 0
	deadline := time.After(window)
drain:
	for {
		select {
		case ev := <-cl.Events():
			if ev.Event == ChippyStateEvent {
				n++
			}
		case <-deadline:
			break drain
		}
	}
	_, _ = cl.Request("pause", map[string]any{"threadId": 1})

	// ~60 Hz over 250 ms ≈ 15 events. Allow generous slack but reject the
	// per-instruction firehose (which would be thousands).
	if n == 0 {
		t.Fatal("no chippy-state events in the window")
	}
	if n > 60 {
		t.Errorf("got %d events in %v; throttle should cap near 60 Hz (~15)", n, window)
	}
}
