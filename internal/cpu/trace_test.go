package cpu_test

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

// newTracedCPU builds a CPU executing `code` from $8000 with a FileTracer
// pointed at a fresh temp file. Returns the CPU, tracer, and the path so
// the test can inspect the file after Close.
func newTracedCPU(t *testing.T, code []byte) (*cpu.CPU, *cpu.FileTracer, string) {
	t.Helper()
	ram := cpu.NewRAM()
	ram.Load(0x8000, code)
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram)

	dir := t.TempDir()
	path := filepath.Join(dir, "trace.log")
	tr := cpu.NewFileTracer()
	if err := tr.SetPath(path); err != nil {
		t.Fatalf("SetPath: %v", err)
	}
	c.Tracer = tr
	return c, tr, path
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open trace: %v", err)
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	if err := s.Err(); err != nil {
		t.Fatalf("scan trace: %v", err)
	}
	return lines
}

func TestTraceDisabledByDefault(t *testing.T) {
	c, tr, path := newTracedCPU(t, []byte{0xEA, 0xEA}) // NOP NOP
	if tr.Enabled() {
		t.Fatalf("tracer should start disabled")
	}
	c.Step()
	c.Step()
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := readLines(t, path); len(got) != 0 {
		t.Fatalf("expected no trace lines while disabled, got %d", len(got))
	}
}

func TestTraceLineFormat(t *testing.T) {
	// LDA #$42 ; NOP ; JMP $8000
	c, tr, path := newTracedCPU(t, []byte{0xA9, 0x42, 0xEA, 0x4C, 0x00, 0x80})
	tr.Enable()
	c.Step() // LDA
	c.Step() // NOP
	c.Step() // JMP
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %v", len(lines), lines)
	}

	// First line: LDA #$42 — 2 bytes (A9 42), regs all zero at reset, P=24
	// (FlagU|FlagI), SP=FD, CYC=7 (the reset-baseline cycles set by Reset()).
	want := regexp.MustCompile(
		`^8000 +A9 42 +LDA +#\$42 +A:00 X:00 Y:00 P:24 SP:FD CYC:7$`)
	if !want.MatchString(lines[0]) {
		t.Fatalf("line 0 format mismatch:\n got: %q\nwant pattern: %s", lines[0], want)
	}

	// Second line: NOP at $8002, A now $42 (post-LDA), Z=0 N=0 still 24.
	// LDA #imm takes 2 cycles; reset baseline 7 -> CYC:9.
	if !strings.Contains(lines[1], "8002  EA      ") {
		t.Fatalf("line 1 missing NOP at $8002: %q", lines[1])
	}
	if !strings.Contains(lines[1], "A:42") {
		t.Fatalf("line 1 missing A:42 after LDA: %q", lines[1])
	}
	if !strings.Contains(lines[1], "CYC:9") {
		t.Fatalf("line 1 missing CYC:9: %q", lines[1])
	}

	// Third line: JMP $8000 — 3 bytes 4C 00 80, CYC after NOP = 11.
	if !strings.Contains(lines[2], "8003  4C 00 80  JMP") {
		t.Fatalf("line 2 missing JMP bytes/disasm: %q", lines[2])
	}
	if !strings.Contains(lines[2], "CYC:11") {
		t.Fatalf("line 2 missing CYC:11: %q", lines[2])
	}
}

func TestTraceToggle(t *testing.T) {
	// Five NOPs.
	c, tr, path := newTracedCPU(t, []byte{0xEA, 0xEA, 0xEA, 0xEA, 0xEA})
	tr.Enable()
	c.Step()
	tr.Disable()
	c.Step()
	c.Step()
	tr.Enable()
	c.Step()
	c.Step()
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines := readLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (steps 1,4,5), got %d: %v", len(lines), lines)
	}
	// Lines should be PC=$8000, $8003, $8004 (step 1, then disabled for
	// steps at $8001/$8002, re-enabled at $8003 and $8004).
	for i, prefix := range []string{"8000  ", "8003  ", "8004  "} {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Fatalf("line %d: want prefix %q, got %q", i, prefix, lines[i])
		}
	}
}

func TestTraceFlushOnDisable(t *testing.T) {
	// Without flush-on-disable, buffered writes wouldn't reach disk yet —
	// readLines would return empty. This verifies the buffer is flushed
	// even before Close.
	c, tr, path := newTracedCPU(t, []byte{0xEA, 0xEA})
	tr.Enable()
	c.Step()
	tr.Disable() // must flush
	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("want 1 flushed line after Disable, got %d", len(lines))
	}
	_ = tr.Close()
}

func TestTraceSetPathReopens(t *testing.T) {
	c, tr, _ := newTracedCPU(t, []byte{0xEA, 0xEA, 0xEA, 0xEA})
	tr.Enable()
	c.Step()
	// Redirect to a second file mid-stream.
	dir := t.TempDir()
	path2 := filepath.Join(dir, "trace2.log")
	if err := tr.SetPath(path2); err != nil {
		t.Fatalf("SetPath: %v", err)
	}
	c.Step()
	c.Step()
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	got := readLines(t, path2)
	if len(got) != 2 {
		t.Fatalf("want 2 lines in second file, got %d: %v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "8001  ") {
		t.Fatalf("second file should start at $8001, got %q", got[0])
	}
}

func TestTraceCloseIdempotent(t *testing.T) {
	_, tr, _ := newTracedCPU(t, []byte{0xEA})
	if err := tr.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
