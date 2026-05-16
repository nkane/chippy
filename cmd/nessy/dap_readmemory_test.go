package main

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"os"

	"github.com/nkane/chippy/internal/dap"
	"github.com/nkane/chippy/internal/nes"
)

// readMemory must return cart PRG bytes, not the underlying ram
// (which is zero for the cart range on a NES bus). Without the
// MMIO.Peek routing this test fails: PRG bytes don't live in
// ram.Data, they live in the cart object behind the MMIO peripheral.
// See #220.
func TestDAP_ReadMemory_ReachesCartBytes(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "hello-bg", "hello-bg.nes")
	data, err := os.ReadFile(romPath)
	if err != nil {
		t.Fatalf("read rom: %v", err)
	}
	rom, err := nes.ParseBytes(data)
	if err != nil {
		t.Fatalf("parse rom: %v", err)
	}
	bus, err := buildNES(rom)
	if err != nil {
		t.Fatalf("buildNES: %v", err)
	}

	clientConn, serverConn := net.Pipe()
	srv := dap.NewServer(serverConn, serverConn)
	if err := srv.AttachExisting(dap.AttachConfig{
		CPU:  bus.cpu,
		RAM:  bus.ram,
		MMIO: bus.mmio,
	}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	go func() {
		_ = srv.Serve()
		_ = serverConn.Close()
	}()
	t.Cleanup(func() { _ = serverConn.Close() })

	client := dap.NewClient(clientConn, clientConn)
	defer func() { _ = client.Close() }()
	if _, err := client.Initialize(dap.InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := client.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Drain the initialized + stopped(entry) events.
	deadline := time.After(500 * time.Millisecond)
drain:
	for {
		select {
		case _, ok := <-client.Events():
			if !ok {
				break drain
			}
		case <-deadline:
			break drain
		}
	}

	// Read 16 bytes at $C000 (the cart's reset routine start).
	resp, err := client.Request("readMemory", map[string]any{
		"memoryReference": "$C000",
		"offset":          0,
		"count":           16,
	})
	if err != nil {
		t.Fatalf("readMemory: %v", err)
	}
	if !resp.Success {
		t.Fatalf("readMemory failed: %s", resp.Message)
	}
	raw, err := json.Marshal(resp.Body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	var body struct {
		Address string `json:"address"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(decoded) != 16 {
		t.Fatalf("decoded length = %d; want 16", len(decoded))
	}
	// hello-bg.s reset starts with SEI ($78), CLD ($D8), LDX #$FF
	// ($A2 $FF). If readMemory was still reading raw `ram` we'd see
	// all zeros.
	if decoded[0] != 0x78 {
		t.Errorf("readMemory[$C000] = $%02X; want $78 (SEI)", decoded[0])
	}
	if decoded[1] != 0xD8 {
		t.Errorf("readMemory[$C001] = $%02X; want $D8 (CLD)", decoded[1])
	}
	if decoded[2] != 0xA2 || decoded[3] != 0xFF {
		t.Errorf("readMemory[$C002..$C003] = $%02X $%02X; want $A2 $FF (LDX #$FF)",
			decoded[2], decoded[3])
	}
}
