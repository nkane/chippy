//go:build launcher_integration

// Build-tag-gated end-to-end test that exercises the full
// `chippy -nessy ROM` flow: spawn a real `nessy` binary with
// -wait-for-debugger, dial its DAP listener, attach with
// stopOnEntry, and assert the CPU is paused at the cart's reset
// vector (NOT in the `forever: JMP forever` spin past it).
//
// Run with the nessy binary built at /tmp/nessy (or set
// CHIPPY_NESSY_BIN to a path):
//
//	go build -tags=nessy -o /tmp/nessy ./cmd/nessy
//	go test -tags=launcher_integration -v ./cmd/nessy/... \
//	  -run TestLauncher_PausesAtResetVector
//
// Tagged because the default CI build doesn't produce a `nessy`
// binary (Ebiten + X11 deps). Run locally before tagging a release
// or merging changes that touch the launcher / wait-for-debugger
// path.

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nkane/chippy/internal/dap"
)

func nessyBinaryPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("CHIPPY_NESSY_BIN"); p != "" {
		return p
	}
	candidates := []string{"/tmp/nessy", "./nessy", "/usr/local/bin/nessy"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	t.Skipf("nessy binary not found; build with `go build -tags=nessy -o /tmp/nessy ./cmd/nessy` or set CHIPPY_NESSY_BIN")
	return ""
}

func pickFreePortForTest(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

func spawnNessy(t *testing.T, bin, romPath string, port int) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin, "-rom", romPath, "-dap-port", strconv.Itoa(port), "-wait-for-debugger")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn nessy: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				_ = cmd.Process.Kill()
			}
		}
	})
	return cmd
}

func dialUntilReady(t *testing.T, port int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	target := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		conn, err := net.DialTimeout("tcp", target, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("nessy never opened DAP listener on :%d within %s: %v", port, timeout, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The integration check. Spawn nessy with -wait-for-debugger, attach,
// inspect PC + first instruction bytes. Without the wait flag the CPU
// would race past reset to the JMP-self spin at ~$C097.
func TestLauncher_PausesAtResetVector(t *testing.T) {
	bin := nessyBinaryPath(t)
	rom, err := filepath.Abs(filepath.Join("..", "..", "roms", "demos", "hello-bg", "hello-bg.nes"))
	if err != nil {
		t.Fatalf("abs rom path: %v", err)
	}
	port := pickFreePortForTest(t)
	spawnNessy(t, bin, rom, port)
	dialUntilReady(t, port, 5*time.Second)

	// 250 ms sleep AFTER listener accepts — gives the game loop
	// plenty of opportunity to misbehave if the wait gate is broken.
	// With wait-for-debugger working, PC stays at $C000.
	time.Sleep(250 * time.Millisecond)

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client := dap.NewClient(conn, conn)
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(dap.InitializeArguments{ClientID: "test", AdapterID: "chippy"}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	resp, err := client.Request("attach", map[string]any{"stopOnEntry": true})
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if !resp.Success {
		t.Fatalf("attach refused: %s", resp.Message)
	}

	// Pull PC + first 8 bytes from $C000.
	stResp, err := client.Request("stackTrace", map[string]any{"threadId": 1, "startFrame": 0, "levels": 1})
	if err != nil {
		t.Fatalf("stackTrace: %v", err)
	}
	if !stResp.Success {
		t.Fatalf("stackTrace failed: %s", stResp.Message)
	}
	stBody, _ := json.Marshal(stResp.Body)
	var st struct {
		StackFrames []struct {
			InstructionPointerReference string `json:"instructionPointerReference"`
		} `json:"stackFrames"`
	}
	_ = json.Unmarshal(stBody, &st)
	if len(st.StackFrames) == 0 {
		t.Fatalf("stackTrace returned no frames")
	}
	pcStr := strings.TrimPrefix(st.StackFrames[0].InstructionPointerReference, "$")
	pc, err := strconv.ParseUint(pcStr, 16, 16)
	if err != nil {
		t.Fatalf("parse PC: %v", err)
	}
	if pc != 0xC000 {
		t.Errorf("PC = $%04X; want $C000 (reset vector). Game loop ran past reset before attach — wait-for-debugger gate is broken", pc)
	}

	// First 8 bytes at $C000 must be reset routine, not BRK fill.
	memResp, err := client.Request("readMemory", map[string]any{
		"memoryReference": "$C000",
		"offset":          0,
		"count":           8,
	})
	if err != nil {
		t.Fatalf("readMemory: %v", err)
	}
	if !memResp.Success {
		t.Fatalf("readMemory failed: %s", memResp.Message)
	}
	memBody, _ := json.Marshal(memResp.Body)
	var mem struct {
		Data string `json:"data"`
	}
	_ = json.Unmarshal(memBody, &mem)
	decoded, err := base64.StdEncoding.DecodeString(mem.Data)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	// hello-bg.s reset:  SEI / CLD / LDX #$FF / TXS / INX / STX $2000
	wantPrefix := []byte{0x78, 0xD8, 0xA2, 0xFF, 0x9A, 0xE8, 0x8E, 0x00}
	for i, want := range wantPrefix {
		if decoded[i] != want {
			t.Errorf("readMemory[$C00%X] = $%02X; want $%02X (reset routine bytes)", i, decoded[i], want)
		}
	}

	// Step once. Expect PC to advance by exactly the SEI instruction
	// width (1 byte).
	if _, err := client.Request("stepIn", map[string]any{"threadId": 1}); err != nil {
		t.Fatalf("stepIn: %v", err)
	}
	// Drain events briefly so the server's stopped event lands.
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	gotStop := false
drainLoop:
	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				break drainLoop
			}
			if ev.Event == "stopped" {
				gotStop = true
				break drainLoop
			}
		case <-timer.C:
			t.Fatalf("no stopped event 2s after stepIn")
		}
	}
	if !gotStop {
		t.Fatal("no stopped event")
	}

	stResp2, _ := client.Request("stackTrace", map[string]any{"threadId": 1, "startFrame": 0, "levels": 1})
	stBody2, _ := json.Marshal(stResp2.Body)
	_ = json.Unmarshal(stBody2, &st)
	pcStr2 := strings.TrimPrefix(st.StackFrames[0].InstructionPointerReference, "$")
	pc2, _ := strconv.ParseUint(pcStr2, 16, 16)
	if pc2 != 0xC001 {
		t.Errorf("PC after stepIn = $%04X; want $C001 (SEI is 1 byte). Game loop racing the step request", pc2)
	}

	// Step several more times. The hello-bg reset routine is:
	//   $C000 78        SEI       → next $C001 (1 byte)
	//   $C001 D8        CLD       → next $C002
	//   $C002 A2 FF     LDX #$FF  → next $C004
	//   $C004 9A        TXS       → next $C005
	//   $C005 E8        INX       → next $C006
	// Each stepIn must advance by exactly the instruction width.
	// If the game loop is racing the dispatch, we'd see PCs leap by
	// ~30k cycles ($C000 → $C097-ish).
	wantPCs := []uint16{0xC002, 0xC004, 0xC005, 0xC006}
	for _, want := range wantPCs {
		if _, err := client.Request("stepIn", map[string]any{"threadId": 1}); err != nil {
			t.Fatalf("stepIn: %v", err)
		}
		stopTimer := time.NewTimer(2 * time.Second)
	stepDrain:
		for {
			select {
			case ev, ok := <-client.Events():
				if !ok {
					stopTimer.Stop()
					break stepDrain
				}
				if ev.Event == "stopped" {
					stopTimer.Stop()
					break stepDrain
				}
			case <-stopTimer.C:
				t.Fatalf("no stopped event 2s after stepIn (target PC $%04X)", want)
			}
		}
		st3Resp, _ := client.Request("stackTrace", map[string]any{"threadId": 1, "startFrame": 0, "levels": 1})
		st3Body, _ := json.Marshal(st3Resp.Body)
		_ = json.Unmarshal(st3Body, &st)
		pcStr3 := strings.TrimPrefix(st.StackFrames[0].InstructionPointerReference, "$")
		pc3, _ := strconv.ParseUint(pcStr3, 16, 16)
		if pc3 != uint64(want) {
			t.Errorf("multi-step regression: PC = $%04X; want $%04X. The game loop is racing the dispatch", pc3, want)
		}
	}
}

// User-reported regression: `:bp clear_ram` followed by `r` did NOT
// break at the bp — server ran past it. Root cause was `:bp` not
// syncing the new breakpoint to the server via
// setInstructionBreakpoints.
//
// This test mirrors the wire-level behavior: setInstructionBreakpoints
// + continue should yield a `stopped` event at the bp's PC.
func TestLauncher_BreakpointStopsContinue(t *testing.T) {
	bin := nessyBinaryPath(t)
	rom, err := filepath.Abs(filepath.Join("..", "..", "roms", "demos", "hello-bg", "hello-bg.nes"))
	if err != nil {
		t.Fatalf("abs rom path: %v", err)
	}
	port := pickFreePortForTest(t)
	spawnNessy(t, bin, rom, port)
	dialUntilReady(t, port, 5*time.Second)

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client := dap.NewClient(conn, conn)
	defer func() { _ = client.Close() }()

	if _, err := client.Initialize(dap.InitializeArguments{}); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := client.Request("attach", map[string]any{"stopOnEntry": true}); err != nil {
		t.Fatalf("attach: %v", err)
	}
	// Drain the initial stopped(entry) event so the bp-stopped event
	// is the only one we care about below.
	deadline := time.After(2 * time.Second)
initialDrain:
	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				break initialDrain
			}
			if ev.Event == "stopped" {
				break initialDrain
			}
		case <-deadline:
			t.Fatalf("no initial stopped event")
		}
	}

	// $C014 lies somewhere inside the `bit $2002 / bpl :-` vblank
	// wait. After continue, the CPU loops there constantly; the bp
	// must trip within microseconds.
	const bpPC = 0xC014
	resp, err := client.Request("setInstructionBreakpoints", map[string]any{
		"breakpoints": []map[string]string{
			{"instructionReference": fmt.Sprintf("$%04X", bpPC)},
		},
	})
	if err != nil {
		t.Fatalf("setInstructionBreakpoints: %v", err)
	}
	if !resp.Success {
		t.Fatalf("setInstructionBreakpoints refused: %s", resp.Message)
	}

	if _, err := client.Request("continue", map[string]any{"threadId": 1}); err != nil {
		t.Fatalf("continue: %v", err)
	}
	stopT := time.NewTimer(5 * time.Second)
	defer stopT.Stop()
	for {
		select {
		case ev, ok := <-client.Events():
			if !ok {
				t.Fatalf("client events closed before bp-stopped event")
			}
			if ev.Event == "stopped" {
				goto stopped
			}
		case <-stopT.C:
			t.Fatalf("continue ran 5s without stopping at bp $%04X — breakpoint sync regressed", bpPC)
		}
	}
stopped:
	stResp, _ := client.Request("stackTrace", map[string]any{"threadId": 1, "startFrame": 0, "levels": 1})
	stBody, _ := json.Marshal(stResp.Body)
	var st struct {
		StackFrames []struct {
			InstructionPointerReference string `json:"instructionPointerReference"`
		} `json:"stackFrames"`
	}
	_ = json.Unmarshal(stBody, &st)
	pcStr := strings.TrimPrefix(st.StackFrames[0].InstructionPointerReference, "$")
	pc, _ := strconv.ParseUint(pcStr, 16, 16)
	if pc != uint64(bpPC) {
		t.Errorf("after continue, PC = $%04X; want $%04X (server should stop at bp)", pc, bpPC)
	}
}
