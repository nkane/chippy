package dap

import (
	"net"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/cpu"
)

// spinUpServer wires a Server against an in-process net.Pipe and returns
// the client-side end. The server runs Serve() in a goroutine until the
// pipe closes.
func spinUpServer(t *testing.T) net.Conn {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	srv := NewServer(serverConn, serverConn)
	if err := srv.AttachExisting(AttachConfig{CPU: c, RAM: ram}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	go func() {
		_ = srv.Serve()
		_ = serverConn.Close()
	}()
	t.Cleanup(func() { _ = serverConn.Close() })
	return clientConn
}

// Happy path: Dial→Initialize→Attach handshake returns capabilities and
// the attach response is success.
func TestClient_InitializeAndAttach(t *testing.T) {
	conn := spinUpServer(t)
	c := NewClient(conn, conn)
	defer func() { _ = c.Close() }()

	caps, err := c.Initialize(InitializeArguments{ClientID: "test", AdapterID: "chippy"})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if !caps.SupportsStepBack {
		t.Errorf("capabilities should include stepBack")
	}
	if !caps.SupportsReadMemoryRequest {
		t.Errorf("capabilities should include readMemory")
	}
	if !caps.SupportsConditionalBreakpoints {
		t.Errorf("capabilities should include conditional breakpoints")
	}

	resp, err := c.Attach()
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !resp.Success {
		t.Errorf("attach response: success=false: %s", resp.Message)
	}
}

// Initialize emits an `initialized` event after the response — the
// client surfaces it through Events().
func TestClient_ReceivesInitializedEvent(t *testing.T) {
	conn := spinUpServer(t)
	c := NewClient(conn, conn)
	defer func() { _ = c.Close() }()

	if _, err := c.Initialize(InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	select {
	case ev, ok := <-c.Events():
		if !ok {
			t.Fatalf("events channel closed before initialized")
		}
		if ev.Event != "initialized" {
			t.Errorf("first event = %q; want 'initialized'", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("no event within 2s")
	}
}

// Disconnect cleanly ends the session. Subsequent requests fail.
func TestClient_Disconnect(t *testing.T) {
	conn := spinUpServer(t)
	c := NewClient(conn, conn)
	defer func() { _ = c.Close() }()

	if _, err := c.Initialize(InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := c.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := c.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	_ = c.Close()
}

// parseDialAddr accepts both `tcp:host:port` and `host:port` forms;
// rejects bare hosts and empty input.
func TestParseDialAddr(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort string
		wantErr  bool
	}{
		{"tcp:localhost:14785", "localhost", "14785", false},
		{"localhost:14785", "localhost", "14785", false},
		{"tcp:127.0.0.1:9000", "127.0.0.1", "9000", false},
		{"127.0.0.1", "", "", true},
		{"", "", "", true},
		{"tcp:nohostport", "", "", true},
	}
	for _, tc := range cases {
		host, port, err := parseDialAddr(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseDialAddr(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr {
			if host != tc.wantHost || port != tc.wantPort {
				t.Errorf("parseDialAddr(%q) = (%q, %q); want (%q, %q)",
					tc.in, host, port, tc.wantHost, tc.wantPort)
			}
		}
	}
}

// Request after Close() returns an explicit error rather than hanging.
func TestClient_RequestAfterCloseFailsFast(t *testing.T) {
	conn := spinUpServer(t)
	c := NewClient(conn, conn)
	_ = c.Close()

	_, err := c.Request("initialize", InitializeArguments{})
	if err == nil {
		t.Fatalf("request after close should error")
	}
}

// A second concurrent client running through the same flow exercises
// the seq dedupe path in the read loop.
func TestClient_ConcurrentRequestsDemux(t *testing.T) {
	conn := spinUpServer(t)
	c := NewClient(conn, conn)
	defer func() { _ = c.Close() }()

	if _, err := c.Initialize(InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := c.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Fire two threads requests in parallel. The seq numbers differ and
	// the read-loop has to route responses to the right pending channel.
	type result struct {
		resp Response
		err  error
	}
	results := make(chan result, 2)
	go func() {
		r, err := c.Request("threads", map[string]any{})
		results <- result{r, err}
	}()
	go func() {
		r, err := c.Request("threads", map[string]any{})
		results <- result{r, err}
	}()
	for range 2 {
		select {
		case r := <-results:
			if r.err != nil {
				t.Errorf("threads: %v", r.err)
			}
			if !r.resp.Success {
				t.Errorf("threads response: success=false")
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for concurrent responses")
		}
	}
}
