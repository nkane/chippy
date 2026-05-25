package dap

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestVars_StackTraceTopFrame(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA, 0xEA})
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stackTrace",
		Arguments:       json.RawMessage(`{"threadId":1}`),
	}
	s.handleStackTrace(req)

	body := out.String()
	if !strings.Contains(body, `"id":0`) {
		t.Fatalf("expected frame id 0, got: %s", body)
	}
	if !strings.Contains(body, `"instructionPointerReference":"$8000"`) {
		t.Fatalf("expected IP $8000 for top frame, got: %s", body)
	}
}

func TestVars_StackTraceWalksJSRFrames(t *testing.T) {
	// JSR $9000 at $8000 ; @$9000: JSR $A000 ; @$A000: NOP
	s, _, out := newStoppedServer(t, []byte{0x20, 0x00, 0x90}) // $8000-$8002
	s.ram.Write(0x9000, 0x20)                                  // JSR
	s.ram.Write(0x9001, 0x00)                                  // $A000 lo
	s.ram.Write(0x9002, 0xA0)                                  // $A000 hi
	s.ram.Write(0xA000, 0xEA)                                  // NOP

	// Step through both JSRs so we're at $A000 with two frames on the stack.
	s.cpu.Step() // JSR $9000 -> PC=$9000
	s.cpu.Step() // JSR $A000 -> PC=$A000
	if s.cpu.PC != 0xA000 {
		t.Fatalf("setup: want PC=$A000, got $%04X", s.cpu.PC)
	}

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "stackTrace",
		Arguments:       json.RawMessage(`{"threadId":1}`),
	}
	s.handleStackTrace(req)

	body := out.String()
	// Expect 3 frames: $A000 (current), $9003 (return from inner), $8003.
	for _, want := range []string{`"$A000"`, `"$9003"`, `"$8003"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected frame ref %s in body:\n%s", want, body)
		}
	}
}

func TestVars_Scopes(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.handleScopes(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "scopes",
		Arguments:       json.RawMessage(`{"frameId":0}`),
	})
	body := out.String()
	if !strings.Contains(body, `"name":"Registers"`) {
		t.Fatalf("missing Registers scope: %s", body)
	}
	if !strings.Contains(body, `"name":"Flags"`) {
		t.Fatalf("missing Flags scope: %s", body)
	}
}

func TestVars_VariablesRegisters(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xA9, 0x42}) // LDA #$42
	s.cpu.Step()                                         // A=$42

	s.handleVariables(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "variables",
		Arguments:       json.RawMessage(`{"variablesReference":1}`),
	})
	body := out.String()
	if !strings.Contains(body, `"name":"A"`) || !strings.Contains(body, `"value":"$42"`) {
		t.Fatalf("expected A=$42 in body:\n%s", body)
	}
}

func TestVars_VariablesFlags(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.cpu.P = 0x24 // FlagU | FlagI

	s.handleVariables(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "variables",
		Arguments:       json.RawMessage(`{"variablesReference":2}`),
	})
	body := out.String()
	// U bit (0x20) and I bit (0x04) should read 1; others 0.
	for _, want := range []string{
		`"name":"U","value":"1"`,
		`"name":"I","value":"1"`,
		`"name":"N","value":"0"`,
		`"name":"C","value":"0"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
		}
	}
}

func TestVars_SetVariableRegister(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xEA})
	s.handleSetVariable(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setVariable",
		Arguments:       json.RawMessage(`{"variablesReference":1,"name":"A","value":"$AA"}`),
	})
	if s.cpu.A != 0xAA {
		t.Fatalf("setVariable A=$AA should write the register; got $%02X", s.cpu.A)
	}
}

func TestVars_SetVariableFlag(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xEA})
	s.cpu.P = 0
	s.handleSetVariable(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setVariable",
		Arguments:       json.RawMessage(`{"variablesReference":2,"name":"C","value":"1"}`),
	})
	if s.cpu.P&0x01 == 0 {
		t.Fatalf("setVariable C=1 should set carry; P=$%02X", s.cpu.P)
	}
	s.handleSetVariable(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setVariable",
		Arguments:       json.RawMessage(`{"variablesReference":2,"name":"C","value":"0"}`),
	})
	if s.cpu.P&0x01 != 0 {
		t.Fatalf("setVariable C=0 should clear carry; P=$%02X", s.cpu.P)
	}
}

func TestVars_SetVariableUnknownReg(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.handleSetVariable(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setVariable",
		Arguments:       json.RawMessage(`{"variablesReference":1,"name":"BOGUS","value":"$01"}`),
	})
	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("unknown reg name should error")
	}
}

func TestParseDAPNumber(t *testing.T) {
	cases := []struct {
		in   string
		want uint64
		err  bool
	}{
		{"$42", 0x42, false},
		{"$FFFF", 0xFFFF, false},
		{"0x80", 0x80, false},
		{"42", 42, false},
		{"FF", 0xFF, false}, // bare hex fallback
		{"", 0, true},
		{"xyz", 0, true},
	}
	for _, c := range cases {
		got, err := parseDAPNumber(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseDAPNumber(%q): want err, got %d", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDAPNumber(%q): unexpected err %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseDAPNumber(%q): want %d, got %d", c.in, c.want, got)
		}
	}
}
