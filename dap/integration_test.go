//go:build integration

// Run with: go test -tags integration ./internal/dap/...
//
// End-to-end DAP session against the real chippy binary over stdio.
// Default `go test` skips this because the spawn + binary build adds ~5s
// per test and isn't needed for the per-handler unit tests in the
// non-tagged files. CI's `integration` job runs it on every commit.

package dap

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// dapClient is a minimal in-test DAP wire driver. Frames JSON in/out via
// Content-Length headers and exposes a request/response/event API.
type dapClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	r      *bufio.Reader
	stderr *strings.Builder

	mu      sync.Mutex
	nextSeq atomic.Int32
	pending map[int]chan map[string]any
	events  chan map[string]any
	done    chan struct{}
}

func newDAPClient(t *testing.T, binPath, romPath string) *dapClient {
	t.Helper()
	cmd := exec.Command(binPath, "-dap", "stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &strings.Builder{}
	cmd.Stderr = struct{ io.Writer }{stderr}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start chippy: %v", err)
	}
	c := &dapClient{
		cmd:     cmd,
		stdin:   stdin,
		r:       bufio.NewReader(stdout),
		stderr:  stderr,
		pending: map[int]chan map[string]any{},
		events:  make(chan map[string]any, 32),
		done:    make(chan struct{}),
	}
	go c.readLoop(t)
	return c
}

func (c *dapClient) readLoop(t *testing.T) {
	defer close(c.done)
	for {
		body, err := ReadMessage(c.r)
		if err != nil {
			return
		}
		var msg map[string]any
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}
		switch msg["type"] {
		case "response":
			seq := int(msg["request_seq"].(float64))
			c.mu.Lock()
			ch, ok := c.pending[seq]
			delete(c.pending, seq)
			c.mu.Unlock()
			if ok {
				ch <- msg
			}
		case "event":
			select {
			case c.events <- msg:
			default:
				// Drop on a full channel — tests should drain.
			}
		}
	}
}

func (c *dapClient) request(t *testing.T, command string, args any) map[string]any {
	t.Helper()
	seq := int(c.nextSeq.Add(1))
	ch := make(chan map[string]any, 1)
	c.mu.Lock()
	c.pending[seq] = ch
	c.mu.Unlock()

	envelope := map[string]any{
		"seq":     seq,
		"type":    "request",
		"command": command,
	}
	if args != nil {
		envelope["arguments"] = args
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal %s: %v", command, err)
	}
	if err := WriteMessage(c.stdin, body); err != nil {
		t.Fatalf("write %s: %v", command, err)
	}
	select {
	case resp := <-ch:
		return resp
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for %s response (stderr: %s)", command, c.stderr.String())
		return nil
	}
}

func (c *dapClient) waitEvent(t *testing.T, name string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case e := <-c.events:
			if e["event"] == name {
				return e
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s event (stderr: %s)", name, c.stderr.String())
			return nil
		}
	}
}

func (c *dapClient) close(t *testing.T) {
	t.Helper()
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
}

// buildChippy builds the chippy binary into a tempdir and returns its
// path. Built once per test (no shared state across t.TempDir() calls).
func buildChippy(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binPath := filepath.Join(dir, "chippy")
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/chippy")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return binPath
}

// writeFixtureROM writes a small program at $8000 and a reset vector
// pointing at it. Returns the absolute path.
//
// Program:
//
//	$8000 A9 42    LDA #$42
//	$8002 A9 77    LDA #$77    <- bp target
//	$8004 A9 00    LDA #$00
//	$8006 4C 00 80 JMP $8000
//
// writeFixtureROM emits a bare-bones .bin containing just the program
// bytes. The loader places them at args.LoadAddr; the reset vector
// resolution falls back to the load address since the .bin doesn't
// carry $FFFC/D bytes.
func writeFixtureROM(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	romPath := filepath.Join(dir, "fixture.bin")
	prog := []byte{0xA9, 0x42, 0xA9, 0x77, 0xA9, 0x00, 0x4C, 0x00, 0x80}
	if err := os.WriteFile(romPath, prog, 0o644); err != nil {
		t.Fatalf("write rom: %v", err)
	}
	return romPath
}

func TestIntegration_FullSession(t *testing.T) {
	binPath := buildChippy(t)
	romPath := writeFixtureROM(t)
	c := newDAPClient(t, binPath, romPath)
	defer c.close(t)

	// initialize
	r := c.request(t, "initialize", map[string]any{"adapterID": "chippy"})
	if r["success"] != true {
		t.Fatalf("initialize failed: %v", r)
	}
	c.waitEvent(t, "initialized", 1*time.Second)

	// launch with rom pointing at the fixture, loadAddr=$8000.
	loadAddr, _ := strconv.ParseUint("8000", 16, 16)
	r = c.request(t, "launch", map[string]any{
		"rom":         romPath,
		"loadAddr":    loadAddr,
		"stopOnEntry": true,
	})
	if r["success"] != true {
		t.Fatalf("launch failed: %v", r)
	}
	stopped := c.waitEvent(t, "stopped", 2*time.Second)
	if reason := stopped["body"].(map[string]any)["reason"]; reason != "entry" {
		t.Fatalf("expected stopped reason=entry, got %v", reason)
	}

	// setInstructionBreakpoints at $8002.
	r = c.request(t, "setInstructionBreakpoints", map[string]any{
		"breakpoints": []map[string]any{
			{"instructionReference": "$8002"},
		},
	})
	if r["success"] != true {
		t.Fatalf("setInstructionBreakpoints failed: %v", r)
	}

	// continue → expect stopped(breakpoint) at $8002.
	r = c.request(t, "continue", map[string]any{"threadId": 1})
	if r["success"] != true {
		t.Fatalf("continue failed: %v", r)
	}
	stopped = c.waitEvent(t, "stopped", 2*time.Second)
	if reason := stopped["body"].(map[string]any)["reason"]; reason != "breakpoint" {
		t.Fatalf("expected stopped reason=breakpoint, got %v", reason)
	}

	// variables of the Registers scope should report A=$42 (LDA #$42
	// at $8000 ran; PC is now at $8002 about to execute LDA #$77).
	r = c.request(t, "variables", map[string]any{"variablesReference": 1})
	body := r["body"].(map[string]any)
	vars := body["variables"].([]any)
	var a, pc string
	for _, v := range vars {
		m := v.(map[string]any)
		switch m["name"] {
		case "A":
			a = m["value"].(string)
		case "PC":
			pc = m["value"].(string)
		}
	}
	if a != "$42" {
		t.Fatalf("A should be $42 after first LDA, got %q", a)
	}
	if pc != "$8002" {
		t.Fatalf("PC should be $8002 at the bp, got %q", pc)
	}

	// stackTrace returns at least one frame at $8002.
	r = c.request(t, "stackTrace", map[string]any{"threadId": 1})
	frames := r["body"].(map[string]any)["stackFrames"].([]any)
	if len(frames) == 0 {
		t.Fatalf("stackTrace returned no frames: %v", r)
	}
	if got := frames[0].(map[string]any)["instructionPointerReference"]; got != "$8002" {
		t.Fatalf("frame 0 IP want $8002, got %v", got)
	}

	// disconnect.
	r = c.request(t, "disconnect", map[string]any{})
	if r["success"] != true {
		t.Fatalf("disconnect failed: %v", r)
	}
	// Confirm chippy exited cleanly.
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		t.Fatalf("chippy didn't exit after disconnect")
	}
}

// sanity-check that the binary built with the build tag is the same one
// the unit-test suite covers — i.e. the integration target hits the
// initialize handler that exists in this branch.
func TestIntegration_BuildSmoke(t *testing.T) {
	binPath := buildChippy(t)
	if fi, err := os.Stat(binPath); err != nil || fi.Size() == 0 {
		t.Fatalf("chippy binary missing or empty: %v %v", fi, err)
	}
	cmd := exec.Command(binPath, "-h")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "-dap") {
		t.Fatalf("chippy -h doesn't mention -dap flag:\n%s", out)
	}
}

func init() {
	// Sanity: keep gofmt happy by referencing fmt so removed imports
	// don't drift. This is also the only place we'd surface a build
	// configuration error to the test runner.
	_ = fmt.Sprintf
}
