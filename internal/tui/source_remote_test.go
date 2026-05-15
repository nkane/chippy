package tui

import (
	"net"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/dap"
)

// spinUpServerCPU builds an in-process DAP server attached to a fresh
// CPU + RAM with a deterministic program (LDA #$42; STA $0200; JMP self)
// loaded at $C000. Returns the client-side net.Conn plus the server's
// CPU pointer so tests can compare authoritative server state against
// the RemoteSource's mirror.
func spinUpServerCPU(t *testing.T) (net.Conn, *cpu.CPU) {
	t.Helper()
	clientConn, serverConn := net.Pipe()

	ram := cpu.NewRAM()
	// Program at $C000: LDA #$42 ($A9 $42) ; STA $0200 ($8D $00 $02) ; JMP self ($4C $05 $C0)
	prog := []byte{0xA9, 0x42, 0x8D, 0x00, 0x02, 0x4C, 0x05, 0xC0}
	ram.Load(0xC000, prog)
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0xC0)
	c := cpu.New(ram)
	c.Reset()

	srv := dap.NewServer(serverConn, serverConn)
	if err := srv.AttachExisting(dap.AttachConfig{CPU: c, RAM: ram}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	go func() {
		_ = srv.Serve()
		_ = serverConn.Close()
	}()
	t.Cleanup(func() { _ = serverConn.Close() })
	return clientConn, c
}

// initAttachedClient drives initialize + attach so the server is in
// the post-handshake state every test starts from.
func initAttachedClient(t *testing.T, conn net.Conn) *dap.Client {
	t.Helper()
	client := dap.NewClient(conn, conn)
	if _, err := client.Initialize(dap.InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := client.Attach(); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Drain the initialized + stopped(attach) events the server sent
	// during the handshake so the test starts with an empty event
	// queue.
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
	return client
}

// Step over the wire: server PC advances 2 (LDA #$42 is 2 bytes), and
// the mirror's PC + A get synced from the server.
func TestRemoteSource_Step_SyncsMirrorFromServer(t *testing.T) {
	conn, serverCPU := spinUpServerCPU(t)
	client := initAttachedClient(t, conn)

	mirrorRAM := cpu.NewRAM()
	mirrorCPU := cpu.New(mirrorRAM)
	src := NewRemoteSource(client, mirrorCPU, mirrorRAM, "tcp:test")
	defer func() { _ = src.Close() }()

	// Initial sync — mirror should reflect server's post-reset state.
	if err := src.RefreshRegs(); err != nil {
		t.Fatalf("initial RefreshRegs: %v", err)
	}
	if mirrorCPU.PC != 0xC000 {
		t.Fatalf("mirror PC after initial refresh = $%04X; want $C000", mirrorCPU.PC)
	}

	// Step. Server's CPU executes one instruction (LDA #$42 → PC=$C002,
	// A=$42). Mirror should match.
	src.Step()

	if serverCPU.PC != 0xC002 {
		t.Errorf("server PC after step = $%04X; want $C002", serverCPU.PC)
	}
	if mirrorCPU.PC != serverCPU.PC {
		t.Errorf("mirror PC = $%04X; server = $%04X — sync failed", mirrorCPU.PC, serverCPU.PC)
	}
	if mirrorCPU.A != serverCPU.A {
		t.Errorf("mirror A = $%02X; server = $%02X — sync failed", mirrorCPU.A, serverCPU.A)
	}
	if mirrorCPU.A != 0x42 {
		t.Errorf("mirror A = $%02X; want $42 (after LDA #$42)", mirrorCPU.A)
	}
}

// SetBreakpoints round-trips. We don't have a way to assert the
// server's internal bp set without exposing it, so the smoke test is
// just "the request succeeds and returns without error".
func TestRemoteSource_SetBreakpoints_NoError(t *testing.T) {
	conn, _ := spinUpServerCPU(t)
	client := initAttachedClient(t, conn)

	mirrorRAM := cpu.NewRAM()
	mirrorCPU := cpu.New(mirrorRAM)
	src := NewRemoteSource(client, mirrorCPU, mirrorRAM, "tcp:test")
	defer func() { _ = src.Close() }()

	if err := src.SetBreakpoints([]uint16{0xC005, 0xC100}); err != nil {
		t.Errorf("SetBreakpoints: %v", err)
	}
}

// Attached + Address are simple getters but worth covering so future
// refactors don't drift.
func TestRemoteSource_AttachedAndAddress(t *testing.T) {
	conn, _ := spinUpServerCPU(t)
	client := initAttachedClient(t, conn)
	src := NewRemoteSource(client, cpu.New(cpu.NewRAM()), cpu.NewRAM(), "tcp:test:1234")
	defer func() { _ = src.Close() }()

	if !src.Attached() {
		t.Errorf("RemoteSource.Attached() = false; want true")
	}
	if src.Address() != "tcp:test:1234" {
		t.Errorf("RemoteSource.Address() = %q; want %q", src.Address(), "tcp:test:1234")
	}
}

// LocalSource is the boring case but worth pinning down: Step should
// advance the CPU's PC by the instruction size.
func TestLocalSource_StepAdvancesPC(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Load(0x8000, []byte{0xEA}) // NOP
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram)
	c.Reset()

	src := NewLocalSource(c, ram)
	src.Step()
	if c.PC != 0x8001 {
		t.Errorf("after NOP step, PC = $%04X; want $8001", c.PC)
	}
}

func TestLocalSource_AttachedFalse(t *testing.T) {
	src := NewLocalSource(cpu.New(cpu.NewRAM()), cpu.NewRAM())
	if src.Attached() {
		t.Errorf("LocalSource.Attached() = true; want false")
	}
	if src.Address() != "" {
		t.Errorf("LocalSource.Address() = %q; want empty", src.Address())
	}
}
