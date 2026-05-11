package cpu

import (
	"bufio"
	"fmt"
	"io"
	"os"
)

// Tracer is an optional execution hook. When set on CPU, Step() calls:
//
//   - LogStep at the instruction boundary just before opcode fetch, so the
//     logged PC/regs reflect the instruction about to execute.
//   - LogInterrupt at the same boundary when an NMI or IRQ is about to be
//     serviced, *before* the 7-cycle service writes PC/P to the stack.
//     This lets viewers spot service boundaries that would otherwise look
//     like an unexplained PC jump in the trace.
//
// Halted-only steps remain silent (no instruction, no interrupt).
type Tracer interface {
	LogStep(c *CPU, bus Bus)
	LogInterrupt(c *CPU, kind string, vector uint16)
}

// FileTracer writes one line per instruction to a file. Cheap when disabled
// (single bool check) so it's safe to leave attached and toggle at runtime.
// Buffer is 64K to keep multi-MHz traces from thrashing syscalls; Close (or
// Disable) flushes.
//
// Line format:
//
//	PC    bytes        disasm           A:xx X:xx Y:xx P:xx SP:xx CYC:n
//	8000  A9 42        LDA  #$42        A:00 X:00 Y:00 P:24 SP:FD CYC:7
//
// Reading opcode bytes goes through the live Bus, so don't enable tracing
// for programs that execute from MMIO regions whose Read has side effects.
type FileTracer struct {
	f       *os.File
	w       *bufio.Writer
	out     io.Writer
	path    string
	enabled bool
}

func NewFileTracer() *FileTracer { return &FileTracer{out: io.Discard} }

// SetPath redirects output to a fresh file at path (truncating any existing
// file). Closes any previous file. The tracer is left in its current
// enabled/disabled state — call Enable() afterwards if needed.
func (t *FileTracer) SetPath(path string) error {
	if err := t.closeFile(); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	t.f = f
	t.w = bufio.NewWriterSize(f, 64*1024)
	t.out = t.w
	t.path = path
	return nil
}

func (t *FileTracer) Path() string  { return t.path }
func (t *FileTracer) Enabled() bool { return t.enabled }
func (t *FileTracer) Enable()       { t.enabled = true }

func (t *FileTracer) Disable() {
	t.enabled = false
	if t.w != nil {
		_ = t.w.Flush()
	}
}

func (t *FileTracer) Close() error {
	t.enabled = false
	return t.closeFile()
}

func (t *FileTracer) closeFile() error {
	if t.w != nil {
		_ = t.w.Flush()
		t.w = nil
	}
	var err error
	if t.f != nil {
		err = t.f.Close()
		t.f = nil
	}
	t.out = io.Discard
	return err
}

func (t *FileTracer) LogInterrupt(c *CPU, kind string, vector uint16) {
	if !t.enabled {
		return
	}
	fmt.Fprintf(t.out, "---- %s -> $%04X (PC=$%04X P=%02X SP=%02X CYC:%d)\n",
		kind, vector, c.PC, c.P, c.SP, c.Cycles)
}

func (t *FileTracer) LogStep(c *CPU, bus Bus) {
	if !t.enabled {
		return
	}
	pc := c.PC
	op := bus.Read(pc)
	in := c.opcodes[op]
	n := min(max(int(in.Bytes), 1), 3)
	var b1, b2 byte
	if n >= 2 {
		b1 = bus.Read(pc + 1)
	}
	if n >= 3 {
		b2 = bus.Read(pc + 2)
	}
	var bytesStr string
	switch n {
	case 1:
		bytesStr = fmt.Sprintf("%02X      ", op)
	case 2:
		bytesStr = fmt.Sprintf("%02X %02X   ", op, b1)
	case 3:
		bytesStr = fmt.Sprintf("%02X %02X %02X", op, b1, b2)
	}
	dis, _ := DisasmCPU(c, pc)
	fmt.Fprintf(t.out, "%04X  %s  %-13s  A:%02X X:%02X Y:%02X P:%02X SP:%02X CYC:%d\n",
		pc, bytesStr, dis, c.A, c.X, c.Y, c.P, c.SP, c.Cycles)
}
