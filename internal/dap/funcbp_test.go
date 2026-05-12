package dap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/symbols"
)

// buildSyms writes a minimal cc65-style .dbg file and loads it. Same
// pattern as the tui complete_test helper; duplicated here to keep
// internal/dap free of cross-package test deps.
func buildSyms(t *testing.T, nameAddr ...interface{}) *symbols.Table {
	t.Helper()
	if len(nameAddr)%2 != 0 {
		t.Fatalf("buildSyms: odd number of name/addr args")
	}
	path := filepath.Join(t.TempDir(), "x.dbg")
	var body []byte
	for i := 0; i < len(nameAddr); i += 2 {
		name := nameAddr[i].(string)
		addr := nameAddr[i+1].(int)
		body = append(body, fmt.Appendf(nil, "sym\tname=\"%s\",val=0x%04X\n", name, addr)...)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write dbg: %v", err)
	}
	tbl, err := symbols.LoadDbg(path)
	if err != nil {
		t.Fatalf("LoadDbg: %v", err)
	}
	return tbl
}

func TestFuncBP_ResolveAndSet(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.syms = buildSyms(t, "main", 0x8042, "render", 0x9000)

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setFunctionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"name":"main"},{"name":"render"}]}`),
	}
	s.handleSetFunctionBreakpoints(req)

	body := out.String()
	if !strings.Contains(body, `"$8042"`) {
		t.Fatalf("expected resolved IP $8042 in body:\n%s", body)
	}
	if !strings.Contains(body, `"$9000"`) {
		t.Fatalf("expected resolved IP $9000 in body:\n%s", body)
	}
	if !s.isBreakpoint(0x8042) || !s.isBreakpoint(0x9000) {
		t.Fatalf("after resolution, both PCs should be in bpHit")
	}
}

func TestFuncBP_UnknownSymbolUnverified(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.syms = buildSyms(t, "main", 0x8042)

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setFunctionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"name":"main"},{"name":"nope"}]}`),
	}
	s.handleSetFunctionBreakpoints(req)

	body := out.String()
	if !strings.Contains(body, `"verified":false`) {
		t.Fatalf("unknown symbol should be verified=false, got: %s", body)
	}
	if !s.isBreakpoint(0x8042) {
		t.Fatalf("known symbol should still resolve even when another in the set fails")
	}
}

func TestFuncBP_NoSymsLoaded(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	// s.syms intentionally nil — no .dbg loaded.

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setFunctionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"name":"main"}]}`),
	}
	s.handleSetFunctionBreakpoints(req)

	body := out.String()
	if !strings.Contains(body, `"verified":false`) {
		t.Fatalf("no-syms-loaded should be verified=false, got: %s", body)
	}
	if !strings.Contains(body, "no symbol table loaded") {
		t.Fatalf("error message should explain the cause, got: %s", body)
	}
}

func TestFuncBP_ReplacesAllForCall(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xEA})
	s.syms = buildSyms(t, "a", 0x8001, "b", 0x8002, "c", 0x8003)

	first := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setFunctionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"name":"a"},{"name":"b"}]}`),
	}
	s.handleSetFunctionBreakpoints(first)
	if !s.isBreakpoint(0x8001) || !s.isBreakpoint(0x8002) {
		t.Fatalf("first set should install a and b")
	}

	// Replace with just c. a and b should drop.
	second := Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setFunctionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"name":"c"}]}`),
	}
	s.handleSetFunctionBreakpoints(second)
	if s.isBreakpoint(0x8001) || s.isBreakpoint(0x8002) {
		t.Fatalf("second set should clear prior function bps")
	}
	if !s.isBreakpoint(0x8003) {
		t.Fatalf("second set should install c")
	}
}

func TestFuncBP_RunStopsAtFunction(t *testing.T) {
	// LDA #$00 (2B) ; LDA #$11 (2B) ; JMP $8000 (3B) — sym `target` at $8002.
	s, _, _ := newStoppedServer(t, []byte{0xA9, 0x00, 0xA9, 0x11, 0x4C, 0x00, 0x80})
	s.syms = buildSyms(t, "target", 0x8002)

	s.handleSetFunctionBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setFunctionBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"name":"target"}]}`),
	})

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	<-s.runDone

	if s.cpu.PC != 0x8002 {
		t.Fatalf("continue+function-bp: want stop at $8002, got $%04X", s.cpu.PC)
	}
}

func TestFuncBP_CapabilityAdvertised(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.handleInitialize(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "initialize",
		Arguments:       json.RawMessage(`{"adapterID":"chippy"}`),
	})
	if !strings.Contains(out.String(), `"supportsFunctionBreakpoints":true`) {
		t.Fatalf("initialize should advertise supportsFunctionBreakpoints:true, got: %s", out.String())
	}
}
