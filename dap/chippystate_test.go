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

// TestFlushDirtyRanges checks the bitmap coalesces into contiguous spans with
// their current bytes, and resets to empty after a flush.
func TestFlushDirtyRanges(t *testing.T) {
	s := newServer()
	s.ram = cpu.NewRAM()
	s.dirty = make([]bool, 0x10000)
	s.dirtyLo, s.dirtyHi = 0x10000, -1
	mark := func(addr uint16, v byte) {
		s.ram.Write(addr, v)
		a := int(addr)
		s.dirty[a] = true
		if a < s.dirtyLo {
			s.dirtyLo = a
		}
		if a > s.dirtyHi {
			s.dirtyHi = a
		}
	}
	// Two clusters: $0010-$0012 contiguous, and a lone $0020.
	mark(0x0010, 0xAA)
	mark(0x0011, 0xBB)
	mark(0x0012, 0xCC)
	mark(0x0020, 0x99)

	ranges := s.flushDirtyRanges()
	if len(ranges) != 2 {
		t.Fatalf("want 2 coalesced ranges, got %d: %+v", len(ranges), ranges)
	}
	if ranges[0].Start != 0x0010 || ranges[0].End != 0x0013 {
		t.Errorf("range[0] = [%04X,%04X); want [0010,0013)", ranges[0].Start, ranges[0].End)
	}
	if string(ranges[0].Data) != string([]byte{0xAA, 0xBB, 0xCC}) {
		t.Errorf("range[0].Data = % X; want AA BB CC", ranges[0].Data)
	}
	if ranges[1].Start != 0x0020 || len(ranges[1].Data) != 1 || ranges[1].Data[0] != 0x99 {
		t.Errorf("range[1] = %+v; want {Start:0020 Data:[99]}", ranges[1])
	}
	// A second flush with no new writes is empty.
	if got := s.flushDirtyRanges(); got != nil {
		t.Errorf("second flush should be empty, got %+v", got)
	}
}

// TestChippyStateDirtyRanges drives a free-run that repeatedly writes $0300 and
// asserts the streamed chippy-state events carry that write inline.
func TestChippyStateDirtyRanges(t *testing.T) {
	ram := cpu.NewRAM()
	// $8000 LDA #$AA ; $8002 STA $0300 ; $8005 JMP $8000
	ram.Load(0x8000, []byte{0xA9, 0xAA, 0x8D, 0x00, 0x03, 0x4C, 0x00, 0x80})
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

	found := false
	deadline := time.After(2 * time.Second)
collect:
	for !found {
		select {
		case ev := <-cl.Events():
			if ev.Event != ChippyStateEvent {
				continue
			}
			cs, ok := ev.Body.(ChippyStateBody)
			if !ok {
				t.Fatalf("body type = %T", ev.Body)
			}
			for _, r := range cs.DirtyRanges {
				if r.Start <= 0x0300 && 0x0300 < r.End {
					if r.Data[0x0300-r.Start] == 0xAA {
						found = true
						break collect
					}
				}
			}
		case <-deadline:
			break collect
		}
	}
	_, _ = cl.Request("pause", map[string]any{"threadId": 1})
	if !found {
		t.Fatal("no chippy-state event carried the $0300=$AA write in dirtyRanges")
	}
}
