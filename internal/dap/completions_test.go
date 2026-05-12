package dap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompletions_IdentifierPrefix(t *testing.T) {
	cases := []struct {
		text       string
		column     int
		wantPrefix string
		wantStart  int
	}{
		// 1-based column = byte index after the prefix.
		{"A + ma", 7, "ma", 5},
		{"ma", 3, "ma", 1},
		{"", 1, "", 1},
		{"A + ", 5, "", 5},
		{"main_loop", 10, "main_loop", 1},
	}
	for _, c := range cases {
		gotPrefix, gotStart := identifierPrefix(c.text, c.column)
		if gotPrefix != c.wantPrefix {
			t.Errorf("identifierPrefix(%q, %d) prefix: want %q, got %q", c.text, c.column, c.wantPrefix, gotPrefix)
		}
		if gotStart != c.wantStart {
			t.Errorf("identifierPrefix(%q, %d) start: want %d, got %d", c.text, c.column, c.wantStart, gotStart)
		}
	}
}

func TestCompletions_RegistersAndFlags(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)

	s.handleCompletions(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "completions",
		Arguments:       json.RawMessage(`{"text":"","column":1}`),
	})

	body := out.String()
	for _, want := range []string{`"label":"A"`, `"label":"X"`, `"label":"PC"`, `"label":"C"`, `"label":"Z"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
		}
	}
}

func TestCompletions_PrefixFilter(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.handleCompletions(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "completions",
		Arguments:       json.RawMessage(`{"text":"P","column":2}`),
	})

	body := out.String()
	if !strings.Contains(body, `"label":"P"`) || !strings.Contains(body, `"label":"PC"`) {
		t.Fatalf("prefix P should match P and PC, got: %s", body)
	}
	if strings.Contains(body, `"label":"A"`) {
		t.Fatalf("prefix P should not match A: %s", body)
	}
}

func TestCompletions_Symbols(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.syms = buildSyms(t, "main", 0x8000, "main_loop", 0x8010, "render", 0x9000)

	s.handleCompletions(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "completions",
		Arguments:       json.RawMessage(`{"text":"ma","column":3}`),
	})

	body := out.String()
	for _, want := range []string{`"label":"main"`, `"label":"main_loop"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
		}
	}
	if strings.Contains(body, `"label":"render"`) {
		t.Fatalf("render shouldn't match prefix ma: %s", body)
	}
}

func TestCompletions_StartReportedForReplacement(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.syms = buildSyms(t, "main", 0x8000)

	// Text "A + ma", column = 7 → prefix "ma" starting at column 5.
	s.handleCompletions(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "completions",
		Arguments:       json.RawMessage(`{"text":"A + ma","column":7}`),
	})

	body := out.String()
	if !strings.Contains(body, `"start":5`) {
		t.Fatalf("expected start=5 for prefix at col 5, got: %s", body)
	}
	if !strings.Contains(body, `"length":2`) {
		t.Fatalf("expected length=2 for 'ma', got: %s", body)
	}
}

func TestCompletions_CapabilityAdvertised(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.handleInitialize(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "initialize",
		Arguments:       json.RawMessage(`{"adapterID":"chippy"}`),
	})
	if !strings.Contains(out.String(), `"supportsCompletionsRequest":true`) {
		t.Fatalf("initialize should advertise supportsCompletionsRequest:true, got: %s", out.String())
	}
}
