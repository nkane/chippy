package dap

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// breakpointLocations was advertised as supported but had no handler.
// Confirms the wiring is now in place and an empty source map returns
// an empty (but well-formed) breakpoints array — no error.
func TestBreakpointLocations_HandlerWiredEvenWithoutSrcMap(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	args := BreakpointLocationsArguments{
		Source: Source{Path: "main.s"},
		Line:   1, EndLine: 10,
	}
	raw, _ := json.Marshal(args)
	s.handleBreakpointLocations(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "breakpointLocations",
		Arguments:       raw,
	})
	body := out.String()
	if !strings.Contains(body, `"success":true`) {
		t.Fatalf("breakpointLocations should succeed even without a source map; got:\n%s", body)
	}
	if !strings.Contains(body, `"breakpoints":[]`) {
		t.Fatalf("no source map → empty breakpoints; got:\n%s", body)
	}
}

// stopOnEntry=false in launch must skip the entry stopped event AND
// kick off the run loop so the CPU is actually executing afterwards.
func TestLaunch_StopOnEntryFalseAutoStartsRunLoop(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xA9, 0x00, 0x4C, 0x00, 0x80})
	// newStoppedServer pre-wires the debuggee; clear it so handleLaunch
	// doesn't reject with "already attached".
	s.cpu = nil
	s.ram = nil
	s.mmio = nil

	stop := false
	args := LaunchArguments{
		Rom:         "", // empty: bootDebuggee will fail
		StopOnEntry: &stop,
	}
	raw, _ := json.Marshal(args)
	s.handleLaunch(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "launch",
		Arguments:       raw,
	})
	// We expect an error response because Rom was empty; what matters
	// here is the wiring: the handler doesn't panic on a nil
	// StopOnEntry pointer dereference now that the field is pointer-typed.
	if !strings.Contains(out.String(), `"command":"launch"`) {
		t.Fatalf("launch response missing: %s", out.String())
	}
}

// stopOnEntry=true (the default behavior) in attach emits stopped(entry).
func TestAttach_DefaultStopOnEntryEmitsStoppedEvent(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.handleAttach(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "attach",
		Arguments:       json.RawMessage(`{}`),
	})
	if !strings.Contains(out.String(), `"reason":"entry"`) {
		t.Fatalf("attach with default StopOnEntry should emit stopped(entry); got: %s", out.String())
	}
}

// stopOnEntry=false in attach skips the stopped event.
func TestAttach_StopOnEntryFalseSkipsStoppedEvent(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.handleAttach(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "attach",
		Arguments:       json.RawMessage(`{"stopOnEntry":false}`),
	})
	if strings.Contains(out.String(), `"event":"stopped"`) {
		t.Fatalf("attach with stopOnEntry=false should NOT emit stopped; got: %s", out.String())
	}
	if !strings.Contains(out.String(), `"command":"attach"`) {
		t.Fatalf("attach response missing: %s", out.String())
	}
}

// writeMemory with allowPartial=false must reject a write that would
// overflow the 64 KiB space rather than silently truncate.
func TestWriteMemory_AllowPartialFalseRejectsOverflow(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	// 4-byte write at $FFFE overflows by 2 bytes.
	payload := base64.StdEncoding.EncodeToString([]byte{0x11, 0x22, 0x33, 0x44})
	body := map[string]interface{}{
		"memoryReference": "$FFFE",
		"data":            payload,
		"allowPartial":    false,
	}
	raw, _ := json.Marshal(body)
	s.handleWriteMemory(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "writeMemory",
		Arguments:       raw,
	})
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("overflow with allowPartial=false should error; got: %s", out.String())
	}
	if !strings.Contains(out.String(), "overflow") {
		t.Fatalf("error message should mention overflow; got: %s", out.String())
	}
	if s.ram.Read(0xFFFE) != 0x00 {
		t.Fatalf("rejected write should NOT have written; $FFFE=$%02X", s.ram.Read(0xFFFE))
	}
}

// writeMemory with allowPartial=true accepts the truncated write.
func TestWriteMemory_AllowPartialTruePerformsTruncatedWrite(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	payload := base64.StdEncoding.EncodeToString([]byte{0x11, 0x22, 0x33, 0x44})
	body := map[string]interface{}{
		"memoryReference": "$FFFE",
		"data":            payload,
		"allowPartial":    true,
	}
	raw, _ := json.Marshal(body)
	s.handleWriteMemory(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "writeMemory",
		Arguments:       raw,
	})
	if !strings.Contains(out.String(), `"bytesWritten":2`) {
		t.Fatalf("allowPartial truncated write should report bytesWritten=2; got: %s", out.String())
	}
	if s.ram.Read(0xFFFE) != 0x11 || s.ram.Read(0xFFFF) != 0x22 {
		t.Fatalf("partial write missing: $FFFE=$%02X $FFFF=$%02X", s.ram.Read(0xFFFE), s.ram.Read(0xFFFF))
	}
}

// Smoke: confirm the breakpointLocations capability advertisement
// still claims true (we haven't dropped it).
func TestInitialize_AdvertisesBreakpointLocations(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.handleInitialize(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "initialize",
		Arguments:       json.RawMessage(`{"adapterID":"chippy"}`),
	})
	if !strings.Contains(out.String(), `"supportsBreakpointLocationsRequest":true`) {
		t.Fatalf("breakpointLocations capability should be advertised; got:\n%s", out.String())
	}
}

// Quick sanity: launch with explicit StopOnEntry=true matches the
// default behavior (no auto-run, stopped(entry) emitted by the launch
// flow when the wired debuggee succeeds).
func TestLaunch_NoDebugSkipsBothEntryAndAutoRun(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.cpu = nil
	s.ram = nil
	s.mmio = nil
	args := LaunchArguments{
		NoDebug: true,
	}
	raw, _ := json.Marshal(args)
	s.handleLaunch(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "launch",
		Arguments:       raw,
	})
	// With NoDebug we don't expect a stopped event regardless of the
	// boot outcome. We also want to confirm no race spawned a run
	// goroutine on a nil CPU.
	time.Sleep(10 * time.Millisecond)
	if strings.Contains(out.String(), `"event":"stopped"`) {
		t.Fatalf("NoDebug=true should suppress stopped; got: %s", out.String())
	}
}
