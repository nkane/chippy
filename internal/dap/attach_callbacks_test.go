package dap

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/cpu"
)

// AttachConfig.OnAttached fires after the client's `attach` request
// completes. OnDisconnected fires on `disconnect` or wire EOF.
// Both are load-bearing for nessy's pause-on-attach semantics
// (issue #212).
func TestAttachConfig_OnAttachedAndOnDisconnected(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	ram := cpu.NewRAM()
	c := cpu.New(ram)

	var attached, disconnected atomic.Int32

	srv := NewServer(serverConn, serverConn)
	cfg := AttachConfig{
		CPU: c,
		RAM: ram,
		OnAttached: func() {
			attached.Add(1)
		},
		OnDisconnected: func() {
			disconnected.Add(1)
		},
	}
	if err := srv.AttachExisting(cfg); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	go func() {
		_ = srv.Serve()
		_ = serverConn.Close()
	}()

	client := NewClient(clientConn, clientConn)
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if attached.Load() != 0 {
		t.Errorf("OnAttached fired before attach request")
	}

	if _, err := client.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// Server fires the callback synchronously inside handleAttach
	// before the response lands, but allow a brief settle for the
	// JSON write to flush.
	deadline := time.After(2 * time.Second)
	for attached.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("OnAttached never fired after successful attach")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	if disconnected.Load() != 0 {
		t.Errorf("OnDisconnected fired before disconnect: count=%d", disconnected.Load())
	}

	if err := client.Disconnect(); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	deadline = time.After(2 * time.Second)
	for disconnected.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("OnDisconnected never fired after client.Disconnect()")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// OnDisconnected must fire exactly once even though Serve()'s
	// EOF exit path and handleDisconnect both call fireDisconnected.
	if got := disconnected.Load(); got != 1 {
		t.Errorf("OnDisconnected fired %d times; want exactly 1", got)
	}
}

// Wire EOF (client closes without sending disconnect) also fires
// OnDisconnected — matches the nessy "user kills chippy TUI" path.
func TestAttachConfig_OnDisconnected_FiresOnEOF(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	ram := cpu.NewRAM()
	c := cpu.New(ram)

	var disconnected atomic.Int32

	srv := NewServer(serverConn, serverConn)
	if err := srv.AttachExisting(AttachConfig{
		CPU: c, RAM: ram,
		OnDisconnected: func() { disconnected.Add(1) },
	}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve()
		close(done)
	}()

	// Close client side without sending disconnect. Server's read
	// loop gets EOF and exits Serve(); deferred fireDisconnected
	// runs.
	_ = clientConn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not exit within 2s after client EOF")
	}
	if got := disconnected.Load(); got != 1 {
		t.Errorf("OnDisconnected fired %d times after EOF; want 1", got)
	}
}
