//go:build klaus

// Klaus Dormann's 6502 interrupt test exercises IRQ, NMI, and BRK under
// various conditions. Unlike the functional / AllSuiteA ROMs it requires a
// feedback port: the program writes a "diag register" at $BFFC to request
// interrupts, and the harness must drive the CPU's IRQ/NMI lines from that
// value.
//
// Klaus ships this test as as65 source only (no bin in bin_files/), so chippy
// vendors a ca65 port — cpu/testdata/6502_interrupt_test.ca65 (+ .cfg) — and
// the assembled bin, SHA-pinned. Rebuild with:
//
//	ca65 6502_interrupt_test.ca65 -o x.o
//	ld65 x.o -o 6502_interrupt_test.bin -C interrupt_test.cfg
//
// Feedback port semantics (I_drive=1 open collector, from the source):
//
//	I_port  = $BFFC   IRQ_bit = 0   NMI_bit = 1
//	a bit driven LOW (0) asserts that interrupt; high (1, the pulled-up
//	idle state) releases it. IRQ is level-sensitive; NMI is edge-triggered
//	on the 1->0 transition. The ROM image fills $BFFC with $FF, so both
//	lines start released.
//
// Success: the test runs twice (clearing expected-interrupt state for the
// 2nd pass) and ends in `JMP *` at one of the `success` macro sites. Any
// other self-loop is a failing-subtest trap.

package cpu

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	irqTestSize   = 65536
	irqTestStart  = 0x0400
	irqTestPort   = 0xBFFC
	irqTestIRQbit = 0
	irqTestNMIbit = 1
	irqTestMaxIns = 10_000_000
)

// irqTestSuccessPCs are the `success` macro self-loop addresses (JMP *) for
// the vendored bin — read from the assembled listing. Reaching any of them
// means every subtest passed.
var irqTestSuccessPCs = map[uint16]bool{0x06F5: true, 0x070F: true, 0x072C: true}

func TestKlausInterruptTest(t *testing.T) {
	bin, err := loadInterruptBinary(t)
	if err != nil {
		t.Skipf("interrupt rom unavailable: %v", err)
	}
	if len(bin) != irqTestSize {
		t.Fatalf("interrupt rom: want %d bytes, got %d", irqTestSize, len(bin))
	}

	ram := NewRAM()
	ram.Load(0x0000, bin)

	c := New(ram) // VariantNMOS
	c.Reset()
	c.PC = irqTestStart

	start := time.Now()
	prevPort := ram.Read(irqTestPort)
	for i := 0; i < irqTestMaxIns; i++ {
		pc := c.PC
		c.Step()

		// Drive interrupt lines from the feedback port. Config is open
		// collector (I_drive=1), no DDR, NMI present: the I_set macro SETs a
		// bit to assert, so the lines are active HIGH. IRQ is level; NMI
		// fires on the 0->1 edge of its bit.
		port := ram.Read(irqTestPort)
		if port&(1<<irqTestIRQbit) != 0 {
			c.AssertIRQ()
		} else {
			c.ReleaseIRQ()
		}
		nmiNow := port&(1<<irqTestNMIbit) != 0
		nmiPrev := prevPort&(1<<irqTestNMIbit) != 0
		if nmiNow && !nmiPrev {
			c.TriggerNMI()
		}
		prevPort = port

		if c.PC == pc { // self-loop: success or failing trap
			if irqTestSuccessPCs[pc] {
				t.Logf("Klaus interrupt test PASSED in %d instructions, %s",
					i+1, time.Since(start).Round(time.Millisecond))
				return
			}
			t.Fatalf("Klaus interrupt test FAILED: trap at PC=$%04X after %d "+
				"instructions  A=%02X X=%02X Y=%02X SP=%02X P=%02X port=$%02X",
				pc, i+1, c.A, c.X, c.Y, c.SP, c.P, port)
		}
	}
	t.Fatalf("Klaus interrupt test did not converge within %d instructions (last PC=$%04X)",
		irqTestMaxIns, c.PC)
}

// loadInterruptBinary reads the assembled bin. The ROM is built from the
// vendored ca65 source (cpu/testdata/6502_interrupt_test.ca65) rather than
// downloaded — CI assembles it with cc65 and points CHIPPY_INTERRUPT_BIN at
// the result. Resolution order:
//  1. CHIPPY_INTERRUPT_BIN env var (CI build, or a local copy)
//  2. cpu/testdata/6502_interrupt_test.bin (if a dev assembled it there)
//
// Build locally with:
//
//	cd cpu/testdata && ca65 6502_interrupt_test.ca65 -o i.o && \
//	  ld65 i.o -o 6502_interrupt_test.bin -C interrupt_test.cfg
func loadInterruptBinary(t *testing.T) ([]byte, error) {
	t.Helper()
	if p := os.Getenv("CHIPPY_INTERRUPT_BIN"); p != "" {
		return os.ReadFile(p)
	}
	return os.ReadFile(filepath.Join("testdata", "6502_interrupt_test.bin"))
}
