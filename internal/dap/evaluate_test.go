package dap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvaluate_RegisterArithmetic(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.cpu.A = 0x11
	s.cpu.X = 0x22

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "evaluate",
		Arguments:       json.RawMessage(`{"expression":"A + X"}`),
	}
	s.handleEvaluate(req)

	body := out.String()
	if !strings.Contains(body, `"result":"$33"`) {
		t.Fatalf("expected $33 result, got: %s", body)
	}
}

func TestEvaluate_MemoryDeref(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.ram.Write(0x0200, 0xAB)

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "evaluate",
		Arguments:       json.RawMessage(`{"expression":"[$0200]"}`),
	}
	s.handleEvaluate(req)

	if !strings.Contains(out.String(), `"result":"$AB"`) {
		t.Fatalf("expected $AB deref result, got: %s", out.String())
	}
}

func TestEvaluate_BadExpression(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "evaluate",
		Arguments:       json.RawMessage(`{"expression":"A +"}`),
	}
	s.handleEvaluate(req)
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("trailing-operator expression should error, got: %s", out.String())
	}
}

func TestEvaluate_HexFormatWidth(t *testing.T) {
	cases := []struct {
		v    uint32
		want string
	}{
		{0x0, "$00"},
		{0x42, "$42"},
		{0xFF, "$FF"},
		{0x0100, "$0100"},
		{0xFFFF, "$FFFF"},
		{0x10000, "$00010000"},
	}
	for _, c := range cases {
		if got := formatEvalResult(c.v); got != c.want {
			t.Errorf("formatEvalResult($%X): want %s, got %s", c.v, c.want, got)
		}
	}
}

func TestEvaluate_EmptyExpressionErrors(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "evaluate",
		Arguments:       json.RawMessage(`{"expression":""}`),
	}
	s.handleEvaluate(req)
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("empty expression should error, got: %s", out.String())
	}
}

func TestEvaluate_PCRegister(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.cpu.PC = 0x8042
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "evaluate",
		Arguments:       json.RawMessage(`{"expression":"PC"}`),
	}
	s.handleEvaluate(req)
	if !strings.Contains(out.String(), `"result":"$8042"`) {
		t.Fatalf("expected PC=$8042, got: %s", out.String())
	}
}
