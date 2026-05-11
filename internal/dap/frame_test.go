package dap

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestFrame_WriteReadRoundTrip(t *testing.T) {
	want := []byte(`{"seq":1,"type":"request","command":"initialize"}`)
	var buf bytes.Buffer
	if err := WriteMessage(&buf, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadMessage(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("roundtrip mismatch:\n want %s\n got  %s", want, got)
	}
}

func TestFrame_ReadIgnoresExtraHeaders(t *testing.T) {
	raw := "Content-Type: application/vscode-jsonrpc\r\n" +
		"Content-Length: 14\r\n" +
		"X-Custom: yes\r\n" +
		"\r\n" +
		"hello, world!\n"
	got, err := ReadMessage(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello, world!\n" {
		t.Fatalf("body mismatch: got %q", got)
	}
}

func TestFrame_ReadMissingLength(t *testing.T) {
	raw := "X-Whatever: yes\r\n\r\nbody"
	if _, err := ReadMessage(bufio.NewReader(strings.NewReader(raw))); err == nil {
		t.Fatalf("expected error on missing Content-Length")
	}
}

func TestFrame_ReadBadLength(t *testing.T) {
	raw := "Content-Length: not-a-number\r\n\r\n"
	if _, err := ReadMessage(bufio.NewReader(strings.NewReader(raw))); err == nil {
		t.Fatalf("expected error on bad Content-Length value")
	}
}

func TestFrame_ReadMultipleMessages(t *testing.T) {
	// Two back-to-back messages share one stream — header parser must not
	// over-read past the declared body.
	var buf bytes.Buffer
	if err := WriteMessage(&buf, []byte("first")); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := WriteMessage(&buf, []byte("second")); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	r := bufio.NewReader(&buf)
	m1, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	m2, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if string(m1) != "first" || string(m2) != "second" {
		t.Fatalf("messages: got %q %q", m1, m2)
	}
}
