package dap

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/cpu"
)

// unixSession listens on a unix-domain socket, serves a NOP-sled CPU, and
// returns a connected wire client. Mirrors spinUpServer but over a real
// socket — the transport the `unix:PATH` CLI mode and out-of-process local
// debugging use.
func unixSession(t *testing.T) *Client {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "chippy.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	ram := cpu.NewRAM()
	for a := 0x8000; a < 0x9000; a++ {
		ram.Write(uint16(a), 0xEA)
	}
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		srv := NewServer(conn, conn)
		_ = srv.AttachExisting(AttachConfig{CPU: c, RAM: ram})
		_ = srv.Serve()
		_ = conn.Close()
	}()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial unix: %v", err)
	}
	cl := NewClient(conn, conn)
	t.Cleanup(func() { _ = cl.Close(); _ = ln.Close() })
	return cl
}

func TestUnixSocket_Session(t *testing.T) {
	cl := unixSession(t)
	if _, err := cl.Initialize(InitializeArguments{AdapterID: "chippy"}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp, err := cl.Attach(); err != nil || !resp.Success {
		t.Fatalf("attach: %v / %+v", err, resp)
	}
	if resp, err := cl.Request("stepIn", nil); err != nil || !resp.Success {
		t.Fatalf("stepIn: %v / %+v", err, resp)
	}
}

// BenchmarkUnixStepIn measures a stepIn round-trip over the unix socket (full
// JSON wire framing) for comparison with the in-process transport.
func BenchmarkUnixStepIn(b *testing.B) {
	sock := filepath.Join(b.TempDir(), "chippy.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	ram := cpu.NewRAM()
	for a := 0x8000; a < 0x9000; a++ {
		ram.Write(uint16(a), 0xEA)
	}
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			return
		}
		srv := NewServer(conn, conn)
		_ = srv.AttachExisting(AttachConfig{CPU: c, RAM: ram})
		_ = srv.Serve()
	}()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		b.Fatalf("dial: %v", err)
	}
	cl := NewClient(conn, conn)
	defer func() { _ = cl.Close() }()
	_, _ = cl.Initialize(InitializeArguments{AdapterID: "chippy"})
	_, _ = cl.Attach()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if c.PC >= 0x8F00 {
			c.PC = 0x8000
		}
		if _, err := cl.Request("stepIn", nil); err != nil {
			b.Fatal(err)
		}
	}
}
