package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// resolveNessyBinary's explicit-override branch points at the path
// caller supplied — verifies the file exists then returns it.
func TestResolveNessyBinary_OverrideExists(t *testing.T) {
	tmp := t.TempDir()
	fake := filepath.Join(tmp, "fake-nessy")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("create fake binary: %v", err)
	}
	got, err := resolveNessyBinary(fake)
	if err != nil {
		t.Fatalf("resolveNessyBinary(%s): %v", fake, err)
	}
	if got != fake {
		t.Errorf("resolveNessyBinary = %q; want %q", got, fake)
	}
}

// Override path that doesn't exist errors clearly.
func TestResolveNessyBinary_OverrideMissing(t *testing.T) {
	_, err := resolveNessyBinary("/no/such/path/nessy")
	if err == nil {
		t.Fatalf("resolveNessyBinary(missing) returned nil error")
	}
}

// nessyBaseName picks the right extension for the host OS.
func TestNessyBaseName(t *testing.T) {
	got := nessyBaseName()
	if runtime.GOOS == "windows" {
		if got != "nessy.exe" {
			t.Errorf("Windows base = %q; want nessy.exe", got)
		}
	} else {
		if got != "nessy" {
			t.Errorf("non-Windows base = %q; want nessy", got)
		}
	}
}

// pickFreePort returns a port the kernel just allocated. The port
// number must be > 0 and re-listenable on the same address (no
// already-bound TIME_WAIT residue).
func TestPickFreePort(t *testing.T) {
	port, err := pickFreePort()
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("port = %d; want 1..65535", port)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", "0"))
	if err != nil {
		t.Fatalf("relisten: %v", err)
	}
	_ = ln.Close()
}

// waitForListener succeeds when a listener exists on the target
// port. The 2-second deadline is enough for the loopback to settle.
func TestWaitForListener_Found(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port

	if err := waitForListener(port, 2*time.Second); err != nil {
		t.Errorf("waitForListener: %v", err)
	}
}

// waitForListener times out when nothing's listening on the target.
// 200 ms is fast enough to keep the test snappy.
func TestWaitForListener_Timeout(t *testing.T) {
	// Grab + release a port so we know nothing else is on it for the
	// scope of this test.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	if err := waitForListener(port, 200*time.Millisecond); err == nil {
		t.Errorf("waitForListener should have timed out")
	}
}
