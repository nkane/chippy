package dap

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

// frameRequest writes a DAP-framed request into buf with the given command,
// seq, and JSON-encoded arguments string. Helper for table-driven server
// tests below.
func frameRequest(t *testing.T, buf *bytes.Buffer, seq int, command, argsJSON string) {
	t.Helper()
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: seq, Type: "request"},
		Command:         command,
		Arguments:       json.RawMessage(argsJSON),
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	if err := WriteMessage(buf, body); err != nil {
		t.Fatalf("write framed request: %v", err)
	}
}

// drainMessages reads every framed JSON message remaining in buf and
// returns them parsed into a generic map shape so assertions can poke at
// specific fields without re-declaring every protocol struct. Reads until
// the underlying buffer reports EOF (bufio.Reader may pre-buffer so we
// can't rely on buf.Len() to detect end-of-stream).
func drainMessages(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	r := bufio.NewReader(buf)
	var out []map[string]any
	for {
		if _, err := r.Peek(1); err != nil {
			return out
		}
		body, err := ReadMessage(r)
		if err != nil {
			t.Fatalf("read framed response: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		out = append(out, m)
	}
}

func TestServer_InitializeHandshake(t *testing.T) {
	// Sequence: initialize → disconnect → EOF. Server should:
	//   1. respond to initialize with success + Capabilities body.
	//   2. emit an "initialized" event.
	//   3. respond to disconnect.
	//   4. exit cleanly (terminated=true).
	var in bytes.Buffer
	frameRequest(t, &in, 1, "initialize", `{"adapterID":"chippy"}`)
	frameRequest(t, &in, 2, "disconnect", `{}`)

	var out bytes.Buffer
	s := NewServer(&in, &out)
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	msgs := drainMessages(t, &out)
	if len(msgs) != 3 {
		t.Fatalf("want 3 outbound messages (init resp, init event, disconnect resp), got %d:\n%v", len(msgs), msgs)
	}

	// Message 0 — initialize response.
	m := msgs[0]
	if m["type"] != "response" || m["command"] != "initialize" {
		t.Fatalf("msg 0 want type=response cmd=initialize, got %v", m)
	}
	if m["success"] != true {
		t.Fatalf("initialize should succeed: %v", m)
	}
	if int(m["request_seq"].(float64)) != 1 {
		t.Fatalf("response request_seq should match request seq: %v", m)
	}
	body, _ := m["body"].(map[string]any)
	if body == nil {
		t.Fatalf("initialize response should carry a Capabilities body")
	}
	if body["supportsConfigurationDoneRequest"] != true {
		t.Fatalf("expected supportsConfigurationDoneRequest=true in capabilities")
	}

	// Message 1 — initialized event.
	if msgs[1]["type"] != "event" || msgs[1]["event"] != "initialized" {
		t.Fatalf("msg 1 want event=initialized, got %v", msgs[1])
	}

	// Message 2 — disconnect response.
	if msgs[2]["type"] != "response" || msgs[2]["command"] != "disconnect" {
		t.Fatalf("msg 2 want type=response cmd=disconnect, got %v", msgs[2])
	}
}

func TestServer_AttachWithoutDebuggeeErrors(t *testing.T) {
	// attach with no AttachExisting call beforehand should report an
	// error explaining the host hasn't wired a debuggee yet.
	var in bytes.Buffer
	frameRequest(t, &in, 1, "attach", `{}`)
	frameRequest(t, &in, 2, "disconnect", `{}`)

	var out bytes.Buffer
	s := NewServer(&in, &out)
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	msgs := drainMessages(t, &out)
	if len(msgs) < 1 {
		t.Fatalf("no response captured")
	}
	if msgs[0]["success"] != false {
		t.Fatalf("attach without debuggee should fail: %v", msgs[0])
	}
	if msgs[0]["message"] == nil {
		t.Fatalf("error response should include a message: %v", msgs[0])
	}
}

func TestServer_LaunchRequiresRom(t *testing.T) {
	// Launch with no rom field — bootDebuggee should refuse, error response.
	var in bytes.Buffer
	frameRequest(t, &in, 1, "launch", `{}`)
	frameRequest(t, &in, 2, "disconnect", `{}`)

	var out bytes.Buffer
	s := NewServer(&in, &out)
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	msgs := drainMessages(t, &out)
	if len(msgs) < 1 {
		t.Fatalf("no response captured")
	}
	if msgs[0]["success"] != false {
		t.Fatalf("launch without rom should fail: %v", msgs[0])
	}
}

func TestServer_UnknownCommand(t *testing.T) {
	var in bytes.Buffer
	frameRequest(t, &in, 1, "bogusCommand", `{}`)
	frameRequest(t, &in, 2, "disconnect", `{}`)

	var out bytes.Buffer
	s := NewServer(&in, &out)
	if err := s.Serve(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	msgs := drainMessages(t, &out)
	if msgs[0]["success"] != false {
		t.Fatalf("unknown command should produce error response: %v", msgs[0])
	}
}
