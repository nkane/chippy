package dap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

func TestDataBP_InfoResolvesAddressAndSymbol(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})

	// Hex address resolves to itself.
	s.handleDataBreakpointInfo(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "dataBreakpointInfo",
		Arguments:       json.RawMessage(`{"name":"$0200"}`),
	})
	body := out.String()
	if !strings.Contains(body, `"dataId":"$0200"`) {
		t.Fatalf("address should resolve to dataId $0200:\n%s", body)
	}
	for _, at := range []string{`"read"`, `"write"`, `"readWrite"`} {
		if !strings.Contains(body, at) {
			t.Errorf("expected access type %s in:\n%s", at, body)
		}
	}

	// Unknown name → null dataId (not settable).
	out.Reset()
	s.handleDataBreakpointInfo(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "dataBreakpointInfo",
		Arguments:       json.RawMessage(`{"name":"not_a_symbol"}`),
	})
	if !strings.Contains(out.String(), `"dataId":null`) {
		t.Fatalf("unknown name should give null dataId:\n%s", out.String())
	}
}

func TestDataBP_RunStopsOnWrite(t *testing.T) {
	// STA $0200 ; JMP $8003 (self-loop)
	s, _, out := newStoppedServer(t, []byte{0x8D, 0x00, 0x02, 0x4C, 0x03, 0x80})

	s.handleSetDataBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setDataBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"dataId":"$0200","accessType":"write"}]}`),
	})

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	<-s.runDone

	if !strings.Contains(out.String(), `"reason":"data breakpoint"`) {
		t.Fatalf("run should stop with reason 'data breakpoint':\n%s", out.String())
	}
	if s.cpu.PC != 0x8003 {
		t.Fatalf("write watch should stop just after STA (PC=$8003), got $%04X", s.cpu.PC)
	}
}

func TestDataBP_RunStopsOnRead(t *testing.T) {
	// LDA $0200 ; JMP $8003 (self-loop)
	s, _, out := newStoppedServer(t, []byte{0xAD, 0x00, 0x02, 0x4C, 0x03, 0x80})

	s.handleSetDataBreakpoints(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setDataBreakpoints",
		Arguments:       json.RawMessage(`{"breakpoints":[{"dataId":"$0200","accessType":"read"}]}`),
	})

	s.handleContinue(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "continue",
	})
	<-s.runDone

	if !strings.Contains(out.String(), `"reason":"data breakpoint"`) {
		t.Fatalf("read watch should stop the run:\n%s", out.String())
	}
}

func TestDataBP_Matches(t *testing.T) {
	cases := []struct {
		access      DataBreakpointAccessType
		kind        cpu.AccessKind
		wantTrigger bool
	}{
		{DataAccessWrite, cpu.AccessWrite, true},
		{DataAccessWrite, cpu.AccessRead, false},
		{DataAccessRead, cpu.AccessRead, true},
		{DataAccessRead, cpu.AccessWrite, false},
		{DataAccessReadWrite, cpu.AccessRead, true},
		{DataAccessReadWrite, cpu.AccessWrite, true},
		{DataAccessWrite, cpu.AccessExec, false},
		{DataAccessReadWrite, cpu.AccessExec, false},
	}
	for _, c := range cases {
		d := &dataBP{access: c.access}
		if got := d.matches(c.kind); got != c.wantTrigger {
			t.Errorf("access=%s kind=%v: matches=%v, want %v", c.access, c.kind, got, c.wantTrigger)
		}
	}
}
