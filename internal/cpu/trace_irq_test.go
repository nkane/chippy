package cpu_test

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

func newTracedCPUForIRQ(t *testing.T) (*cpu.CPU, *cpu.FileTracer, string) {
	t.Helper()
	ram := cpu.NewRAM()
	// Trivial program: NOP NOP NOP (so Step has something to do after service).
	ram.Load(0x8000, []byte{0xEA, 0xEA, 0xEA})
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	// Vector handlers: NMI at $9000, IRQ at $A000.
	ram.Write(cpu.VecNMI, 0x00)
	ram.Write(cpu.VecNMI+1, 0x90)
	ram.Write(cpu.VecIRQ, 0x00)
	ram.Write(cpu.VecIRQ+1, 0xA0)
	ram.Load(0x9000, []byte{0x40}) // RTI
	ram.Load(0xA000, []byte{0x40}) // RTI

	dir := t.TempDir()
	path := filepath.Join(dir, "trace.log")
	tr := cpu.NewFileTracer()
	if err := tr.SetPath(path); err != nil {
		t.Fatalf("SetPath: %v", err)
	}
	tr.Enable()
	c := cpu.New(ram)
	c.Tracer = tr
	return c, tr, path
}

func readTraceLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	var lines []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	return lines
}

func TestTrace_NMIEntryLine(t *testing.T) {
	c, tr, path := newTracedCPUForIRQ(t)
	c.TriggerNMI()
	c.Step() // services NMI, no instruction
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines := readTraceLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("want exactly 1 trace line (NMI marker), got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "---- NMI -> $FFFA") {
		t.Fatalf("expected NMI marker, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "PC=$8000") {
		t.Fatalf("expected pre-service PC=$8000 in marker, got %q", lines[0])
	}
}

func TestTrace_IRQEntryLine(t *testing.T) {
	c, tr, path := newTracedCPUForIRQ(t)
	c.P &^= 0x04 // clear FlagI so IRQ is accepted
	c.AssertIRQ()
	c.Step() // services IRQ
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines := readTraceLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "---- IRQ -> $FFFE") {
		t.Fatalf("expected IRQ marker, got %q", lines[0])
	}
}

func TestTrace_NMIThenHandlerInstruction(t *testing.T) {
	// Verify the marker precedes the first handler instruction in the trace,
	// so a reader can see the service boundary right before the vector code.
	c, tr, path := newTracedCPUForIRQ(t)
	c.TriggerNMI()
	c.Step() // marker only (service)
	c.Step() // RTI at $9000 — but wait, that returns to the original PC.
	// Actually we want to verify the marker shows BEFORE the handler's first
	// instruction. Service step doesn't fetch an instruction; the next Step
	// fetches RTI. So expect: [marker, RTI line].
	if err := tr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines := readTraceLines(t, path)
	if len(lines) < 2 {
		t.Fatalf("want at least 2 lines, got %d: %v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[0], "---- NMI") {
		t.Fatalf("line 0 should be NMI marker, got %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "9000 ") {
		t.Fatalf("line 1 should be RTI at $9000, got %q", lines[1])
	}
}
