package dap

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nkane/chippy/cpu"
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

// Probe-style connections that open + close without sending an attach
// request must NOT fire OnDisconnected. The launcher's waitForListener
// dial-and-close was leaving nessy's dapAttached counter at -1, which
// then offset the real attach to 0 (instead of 1) — game loop fell
// through both gates and stepped the CPU autonomously past reset.
func TestAttachConfig_ProbeConnectionDoesNotFireOnDisconnected(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	ram := cpu.NewRAM()
	c := cpu.New(ram)

	var attached, disconnected atomic.Int32

	srv := NewServer(serverConn, serverConn)
	if err := srv.AttachExisting(AttachConfig{
		CPU: c, RAM: ram,
		OnAttached:     func() { attached.Add(1) },
		OnDisconnected: func() { disconnected.Add(1) },
	}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve()
		close(done)
	}()

	// Close the client end immediately — no Initialize, no Attach.
	// This is the shape of the launcher's listener-probe dial.
	_ = clientConn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not exit within 2s after probe close")
	}
	if got := attached.Load(); got != 0 {
		t.Errorf("OnAttached fired %d times for probe-only connection; want 0", got)
	}
	if got := disconnected.Load(); got != 0 {
		t.Errorf("OnDisconnected fired %d times for probe-only connection; want 0 — host counters must stay balanced", got)
	}
}

// Wire EOF AFTER a successful attach (e.g. user kills the chippy TUI)
// fires OnDisconnected so the host can rebalance any counters.
func TestAttachConfig_OnDisconnected_FiresOnEOF(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	ram := cpu.NewRAM()
	c := cpu.New(ram)

	var attached, disconnected atomic.Int32

	srv := NewServer(serverConn, serverConn)
	if err := srv.AttachExisting(AttachConfig{
		CPU: c, RAM: ram,
		OnAttached:     func() { attached.Add(1) },
		OnDisconnected: func() { disconnected.Add(1) },
	}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve()
		close(done)
	}()

	// Drive a real attach first so the OnAttached/OnDisconnected
	// pairing is satisfied. Without this, fireDisconnected (per the
	// pairing guard) wouldn't fire on EOF — see the probe-only test
	// above.
	client := NewClient(clientConn, clientConn)
	if _, err := client.Initialize(InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := client.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}
	deadline := time.After(2 * time.Second)
	for attached.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("OnAttached never fired")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Now close the client side without a disconnect request —
	// Serve's read loop sees EOF, exits, fireDisconnected runs.
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
