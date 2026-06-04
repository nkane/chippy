package dap

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/nkane/chippy/cpu"
)

// AttachConfig.CustomRequestHandler receives request commands the
// built-in dispatch doesn't recognize. A handled=true response with no
// error is sent back as a normal success response carrying the body;
// handled=false falls through to the standard "not implemented" error;
// handled=true with an error sends an error response. This is the
// extension point nessy uses to expose PPU / OAM / mapper debug state
// over `nessy/*` requests without forking the protocol.
func TestCustomRequestHandler(t *testing.T) {
	clientConn, serverConn := net.Pipe()

	ram := cpu.NewRAM()
	c := cpu.New(ram)
	var cpuMu sync.Mutex

	// lockedDuringHandler records whether the CPU lock was already held
	// when the handler ran — it must be, so a handler reading live state
	// observes a coherent snapshot rather than a mid-instruction one.
	var lockedDuringHandler bool

	srv := NewServer(serverConn, serverConn)
	cfg := AttachConfig{
		CPU:   c,
		RAM:   ram,
		CPUMu: &cpuMu,
		CustomRequestHandler: func(command string, args json.RawMessage) (any, bool, error) {
			switch command {
			case "nessy/ping":
				// dispatch holds cpuMu around the fallback path; a
				// TryLock here must fail, proving the handler runs locked.
				if cpuMu.TryLock() {
					cpuMu.Unlock()
					lockedDuringHandler = false
				} else {
					lockedDuringHandler = true
				}
				var in struct {
					Echo string `json:"echo"`
				}
				_ = json.Unmarshal(args, &in)
				return map[string]any{"pong": in.Echo}, true, nil
			case "nessy/boom":
				return nil, true, fmt.Errorf("kaboom")
			default:
				return nil, false, nil // defer to "not implemented"
			}
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
	if _, err := client.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// handled=true, no error → success response with body.
	resp, err := client.Request("nessy/ping", map[string]string{"echo": "hi"})
	if err != nil {
		t.Fatalf("nessy/ping: %v", err)
	}
	if !resp.Success {
		t.Fatalf("nessy/ping: Success=false, message=%q", resp.Message)
	}
	raw, _ := json.Marshal(resp.Body)
	var got struct {
		Pong string `json:"pong"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.Pong != "hi" {
		t.Errorf("nessy/ping body pong=%q; want %q", got.Pong, "hi")
	}
	if !lockedDuringHandler {
		t.Error("custom handler ran without the CPU lock held; snapshot would not be coherent")
	}

	// handled=true with error → error response carrying the message.
	resp, err = client.Request("nessy/boom", nil)
	if err != nil {
		t.Fatalf("nessy/boom transport: %v", err)
	}
	if resp.Success {
		t.Error("nessy/boom: Success=true; want error response")
	}
	if resp.Message != "kaboom" {
		t.Errorf("nessy/boom message=%q; want %q", resp.Message, "kaboom")
	}

	// handled=false → standard "not implemented" error.
	resp, err = client.Request("totally/unknown", nil)
	if err != nil {
		t.Fatalf("unknown transport: %v", err)
	}
	if resp.Success {
		t.Error("unknown command: Success=true; want not-implemented error")
	}
}

// With no CustomRequestHandler set, unknown commands still produce the
// standard "not implemented" error — the extension point is opt-in.
func TestCustomRequestHandler_NilDefersToNotImplemented(t *testing.T) {
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

	client := NewClient(clientConn, clientConn)
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := client.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}

	resp, err := client.Request("nessy/ping", nil)
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	if resp.Success {
		t.Error("unknown command with nil handler: Success=true; want not-implemented error")
	}
}
