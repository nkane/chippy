package dap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nkane/chippy/symbols"
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
	// The two JSR-pair frames carry their stack-page slot (issue #449); frame
	// 0 (the live PC) does not. After two JSRs the return pairs sit near the
	// top of the page: $01FC (outer, ret $8003) and $01FA (inner, ret $9003).
	for _, want := range []string{`"chippyStackAddr":"$01FC"`, `"chippyStackAddr":"$01FA"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
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

// withSyms loads a tiny .dbg into the server so the Globals scope (issue
// #410) has data symbols to enumerate.
func withSyms(t *testing.T, s *Server, dbg string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.dbg")
	if err := os.WriteFile(path, []byte(dbg), 0o644); err != nil {
		t.Fatalf("write dbg: %v", err)
	}
	tbl, err := symbols.LoadDbg(path)
	if err != nil {
		t.Fatalf("load dbg: %v", err)
	}
	s.syms = tbl
}

func varsReq(seq int, args string) Request {
	return Request{
		ProtocolMessage: ProtocolMessage{Seq: seq, Type: "request"},
		Command:         "variables",
		Arguments:       json.RawMessage(args),
	}
}

func TestVars_GlobalsScopeOnlyWhenSyms(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s, "sym\tname=\"score\",val=0x0010,size=1\n")
	s.handleScopes(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "scopes",
		Arguments:       json.RawMessage(`{"frameId":0}`),
	})
	if !strings.Contains(out.String(), `"name":"Globals"`) {
		t.Fatalf("Globals scope missing when syms loaded:\n%s", out.String())
	}
}

func TestVars_GlobalsScalarAndArray(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s,
		"sym\tname=\"score\",val=0x0010,size=1\n"+
			"sym\tname=\"buf\",val=0x0400,size=4\n")
	s.ram.Write(0x0010, 0x7F)

	s.handleVariables(varsReq(1, `{"variablesReference":3}`))
	body := out.String()
	if !strings.Contains(body, `"name":"score"`) || !strings.Contains(body, `"value":"$7F"`) {
		t.Fatalf("scalar global score=$7F missing:\n%s", body)
	}
	// Array global is expandable: nonzero variablesReference + indexedVariables.
	if !strings.Contains(body, `"name":"buf"`) || !strings.Contains(body, `"indexedVariables":4`) {
		t.Fatalf("array global buf missing/not expandable:\n%s", body)
	}
}

func TestVars_GlobalsArrayChildren(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s, "sym\tname=\"buf\",val=0x0400,size=4\n")
	for i := 0; i < 4; i++ {
		s.ram.Write(0x0400+uint16(i), byte(0xA0+i))
	}
	// Enumerate the scope to allocate the array ref.
	s.handleVariables(varsReq(1, `{"variablesReference":3}`))
	if len(s.varRefs) != 1 {
		t.Fatalf("expected 1 dynamic array ref, got %d", len(s.varRefs))
	}
	var ref int
	for r := range s.varRefs {
		ref = r
	}
	out.Reset()
	s.handleVariables(varsReq(2, fmt.Sprintf(`{"variablesReference":%d}`, ref)))
	body := out.String()
	for _, want := range []string{`"name":"[0]","value":"$A0"`, `"name":"[3]","value":"$A3"`} {
		if !strings.Contains(body, want) {
			t.Errorf("array child %s missing:\n%s", want, body)
		}
	}
}

func TestVars_SetVariableGlobalScalar(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s, "sym\tname=\"score\",val=0x0010,size=1\n")
	s.handleSetVariable(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "setVariable",
		Arguments:       json.RawMessage(`{"variablesReference":3,"name":"score","value":"$7F"}`),
	})
	if got := s.ram.Read(0x0010); got != 0x7F {
		t.Fatalf("setVariable score=$7F should poke $0010; got $%02X", got)
	}
	if !strings.Contains(out.String(), `"value":"$7F"`) {
		t.Fatalf("response should echo $7F:\n%s", out.String())
	}
}

func TestVars_SetVariableArrayChild(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s, "sym\tname=\"buf\",val=0x0400,size=4\n")
	// Enumerate the scope so the array ref is allocated.
	s.handleVariables(varsReq(1, `{"variablesReference":3}`))
	var ref int
	for r := range s.varRefs {
		ref = r
	}
	out.Reset()
	s.handleSetVariable(Request{
		ProtocolMessage: ProtocolMessage{Seq: 2, Type: "request"},
		Command:         "setVariable",
		Arguments:       json.RawMessage(fmt.Sprintf(`{"variablesReference":%d,"name":"[2]","value":"$AB"}`, ref)),
	})
	if got := s.ram.Read(0x0402); got != 0xAB {
		t.Fatalf("setVariable buf[2]=$AB should poke $0402; got $%02X", got)
	}
	if !strings.Contains(out.String(), `"value":"$AB"`) {
		t.Fatalf("response should echo $AB:\n%s", out.String())
	}
}

func TestVars_GlobalsArrayPaging(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s, "sym\tname=\"buf\",val=0x0400,size=8\n")
	for i := 0; i < 8; i++ {
		s.ram.Write(0x0400+uint16(i), byte(i))
	}
	s.handleVariables(varsReq(1, `{"variablesReference":3}`))
	var ref int
	for r := range s.varRefs {
		ref = r
	}
	out.Reset()
	// Window [2, 4): indices 2 and 3 only.
	s.handleVariables(varsReq(2, fmt.Sprintf(`{"variablesReference":%d,"start":2,"count":2}`, ref)))
	body := out.String()
	if !strings.Contains(body, `"name":"[2]"`) || !strings.Contains(body, `"name":"[3]"`) {
		t.Fatalf("paged window missing [2]/[3]:\n%s", body)
	}
	if strings.Contains(body, `"name":"[0]"`) || strings.Contains(body, `"name":"[4]"`) {
		t.Fatalf("paged window leaked out-of-range elements:\n%s", body)
	}
}

func TestVars_GlobalsFiltersCodeLabels(t *testing.T) {
	// A sized symbol that maps to a source line is code, not data — dropped.
	s, _, out := newStoppedServer(t, []byte{0xEA})
	withSyms(t, s, "sym\tname=\"main\",val=0x8000,size=20\n")
	s.srcMap = &symbols.SourceMap{PCToSrc: map[uint16]symbols.SrcLoc{0x8000: {File: "main.c", Line: 1}}}
	s.handleVariables(varsReq(1, `{"variablesReference":3}`))
	if strings.Contains(out.String(), `"name":"main"`) {
		t.Fatalf("code label 'main' should be filtered from Globals:\n%s", out.String())
	}
}
