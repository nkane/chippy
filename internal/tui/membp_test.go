// Headless smoke test for memory watchpoints.
//
// Wraps RAM in a WBus, runs hand-crafted 6502 programs, and asserts the
// processMemHits behaviour for each watchpoint variant: plain, read-only,
// write-only, hit-count, one-shot, conditional, and log point.
//
// No TUI runtime is exercised — only the data plane (WBus.Read/Write,
// ring buffer, Model.processMemHits). StatePath is left empty so
// saveState() is a no-op.
package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

// progStaThenLda assembles `LDA #$05; STA $0210; LDA $0210; BRK` at $0200
// and points the reset vector there.
func progStaThenLda(ram *cpu.RAM) {
	p := uint16(0x0200)
	// LDA #$05
	ram.Write(p, 0xA9)
	ram.Write(p+1, 0x05)
	// STA $0210
	ram.Write(p+2, 0x8D)
	ram.Write(p+3, 0x10)
	ram.Write(p+4, 0x02)
	// LDA $0210
	ram.Write(p+5, 0xAD)
	ram.Write(p+6, 0x10)
	ram.Write(p+7, 0x02)
	// BRK
	ram.Write(p+8, 0x00)
	// Reset vector -> $0200
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x02)
}

// progLoopWrites writes #$05 to $0210 four times in a row, then BRK.
//
//	LDA #$05
//	STA $0210 ; x4
//	BRK
func progLoopWrites(ram *cpu.RAM) {
	p := uint16(0x0200)
	ram.Write(p, 0xA9)
	ram.Write(p+1, 0x05)
	for i := 0; i < 4; i++ {
		base := p + 2 + uint16(i*3)
		ram.Write(base, 0x8D)
		ram.Write(base+1, 0x10)
		ram.Write(base+2, 0x02)
	}
	ram.Write(p+14, 0x00) // BRK
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x02)
}

// setup wires RAM, the program, the WBus wrapper, the CPU, and a Model
// in the same configuration as cmd/chippy/main.go.
func setup(t *testing.T, loadProg func(*cpu.RAM)) (*Model, *cpu.CPU) {
	t.Helper()
	ram := cpu.NewRAM()
	loadProg(ram)
	wbus := NewWBus(ram)
	c := cpu.New(wbus) // Reset reads $FFFC via wbus.Read; harmless
	mv := New(c, ram).WithWBus(wbus)
	m := &mv
	return m, c
}

// runUntilBRK steps until the CPU halts (BRK sets Halted) or maxSteps
// elapses, draining hits between each step. Returns aggregated pause
// flag and last status from processMemHits.
func runUntilBRK(t *testing.T, m *Model, maxSteps int) (paused bool, lastStatus string, steps int) {
	t.Helper()
	for steps = 0; steps < maxSteps; steps++ {
		if m.CPU.Halted {
			return
		}
		m.CPU.Step()
		p, s := m.processMemHits()
		if s != "" {
			lastStatus = s
		}
		if p {
			paused = true
			return
		}
	}
	return
}

func TestMemBP_PlainWriteFires(t *testing.T) {
	m, _ := setup(t, progStaThenLda)
	m.MemBPs[0x0210] = newMemBP(0x0210, MemBPWrite)

	paused, status, _ := runUntilBRK(t, m, 100)
	if !paused {
		t.Fatalf("expected pause on STA $0210, got none (status=%q)", status)
	}
	if !strings.Contains(status, "$0210") || !strings.Contains(status, "W") {
		t.Errorf("status missing addr/kind: %q", status)
	}
	if m.MemBPs[0x0210].Hits != 1 {
		t.Errorf("hits=%d, want 1", m.MemBPs[0x0210].Hits)
	}
}

func TestMemBP_WriteOnlyIgnoresReads(t *testing.T) {
	m, c := setup(t, progStaThenLda)
	bp := newMemBP(0x0210, MemBPWrite)
	m.MemBPs[0x0210] = bp

	// Step LDA #$05 (no mem access at $0210)
	c.Step()
	paused, _ := m.processMemHits()
	if paused {
		t.Fatalf("LDA imm should not pause write watch")
	}
	// Step STA $0210 (write hit)
	c.Step()
	paused, _ = m.processMemHits()
	if !paused {
		t.Fatalf("STA $0210 should pause write watch")
	}
	bp.Hits = 0 // reset for next assertion
	// Step LDA $0210 (read; should NOT pause write watch)
	c.Step()
	paused, _ = m.processMemHits()
	if paused {
		t.Fatalf("LDA $0210 must not pause write-only watch")
	}
	if bp.Hits != 0 {
		t.Errorf("write watch counted a read: hits=%d", bp.Hits)
	}
}

func TestMemBP_ReadOnlyIgnoresWrites(t *testing.T) {
	m, c := setup(t, progStaThenLda)
	bp := newMemBP(0x0210, MemBPRead)
	m.MemBPs[0x0210] = bp

	c.Step() // LDA #$05
	_, _ = m.processMemHits()
	c.Step() // STA $0210 — write, must not fire
	paused, _ := m.processMemHits()
	if paused {
		t.Fatalf("STA must not pause read-only watch")
	}
	if bp.Hits != 0 {
		t.Errorf("read-only watch counted a write: hits=%d", bp.Hits)
	}
	c.Step() // LDA $0210 — read, must fire
	paused, status := m.processMemHits()
	if !paused {
		t.Fatalf("LDA $0210 must pause read watch (status=%q)", status)
	}
	if bp.Hits != 1 {
		t.Errorf("hits=%d, want 1", bp.Hits)
	}
}

func TestMemBP_ReadWriteFiresOnBoth(t *testing.T) {
	m, c := setup(t, progStaThenLda)
	bp := newMemBP(0x0210, MemBPReadWrite)
	m.MemBPs[0x0210] = bp

	c.Step() // LDA #$05
	_, _ = m.processMemHits()
	c.Step() // STA — write, fires
	paused, _ := m.processMemHits()
	if !paused {
		t.Fatalf("STA should fire RW watch")
	}
	if bp.Hits != 1 {
		t.Fatalf("hits=%d after STA, want 1", bp.Hits)
	}
	c.Step() // LDA absolute — read, fires again
	paused, _ = m.processMemHits()
	if !paused {
		t.Fatalf("LDA should fire RW watch")
	}
	if bp.Hits != 2 {
		t.Errorf("hits=%d after LDA, want 2", bp.Hits)
	}
}

func TestMemBP_HitCountGate(t *testing.T) {
	m, _ := setup(t, progLoopWrites)
	bp := newMemBP(0x0210, MemBPWrite)
	bp.HitLimit = 3
	m.MemBPs[0x0210] = bp

	paused, _, _ := runUntilBRK(t, m, 200)
	if !paused {
		t.Fatalf("expected pause on 3rd write")
	}
	if bp.Hits != 3 {
		t.Errorf("hits=%d, want 3 (paused on Nth)", bp.Hits)
	}
	// Watch must remain installed (hit-count is not auto-clear)
	if _, ok := m.MemBPs[0x0210]; !ok {
		t.Errorf("hit-count watch should not auto-delete")
	}
}

func TestMemBP_OnceDeletes(t *testing.T) {
	m, _ := setup(t, progLoopWrites)
	bp := newMemBP(0x0210, MemBPWrite)
	bp.HitLimit = -1 // one-shot
	m.MemBPs[0x0210] = bp

	paused, _, _ := runUntilBRK(t, m, 200)
	if !paused {
		t.Fatalf("expected pause on first write")
	}
	if _, ok := m.MemBPs[0x0210]; ok {
		t.Errorf("one-shot watch should be deleted after firing")
	}
}

func TestMemBP_ConditionTrueFires(t *testing.T) {
	m, _ := setup(t, progStaThenLda)
	bp := newMemBP(0x0210, MemBPWrite)
	bp.Cond = "A==$05"
	fn, err := compileCondition(bp.Cond, m.Syms)
	if err != nil {
		t.Fatalf("compile cond: %v", err)
	}
	bp.condFn = fn
	m.MemBPs[0x0210] = bp

	paused, _, _ := runUntilBRK(t, m, 100)
	if !paused {
		t.Fatalf("cond A==$05 must fire (A is $05 at STA)")
	}
}

func TestMemBP_ConditionFalseSuppresses(t *testing.T) {
	m, _ := setup(t, progStaThenLda)
	bp := newMemBP(0x0210, MemBPWrite)
	bp.Cond = "A==$FF"
	fn, err := compileCondition(bp.Cond, m.Syms)
	if err != nil {
		t.Fatalf("compile cond: %v", err)
	}
	bp.condFn = fn
	m.MemBPs[0x0210] = bp

	paused, status, _ := runUntilBRK(t, m, 100)
	if paused {
		t.Fatalf("cond A==$FF must suppress fire (status=%q)", status)
	}
	if bp.Hits != 0 {
		t.Errorf("hits=%d, want 0 when cond false", bp.Hits)
	}
}

func TestMemBP_LogPointDoesNotPause(t *testing.T) {
	m, _ := setup(t, progLoopWrites)
	bp := newMemBP(0x0210, MemBPWrite)
	bp.Log = "wrote A={A}"
	m.MemBPs[0x0210] = bp

	paused, status, _ := runUntilBRK(t, m, 200)
	if paused {
		t.Fatalf("log point must not pause")
	}
	if !strings.Contains(status, "wrote A=") {
		t.Errorf("log status missing template output: %q", status)
	}
	// All 4 writes recorded
	if bp.Hits != 4 {
		t.Errorf("hits=%d, want 4", bp.Hits)
	}
}

func TestMemBP_DisabledIgnored(t *testing.T) {
	m, _ := setup(t, progStaThenLda)
	bp := newMemBP(0x0210, MemBPWrite)
	bp.Enabled = false
	m.MemBPs[0x0210] = bp

	paused, _, _ := runUntilBRK(t, m, 100)
	if paused {
		t.Fatalf("disabled watch must not fire")
	}
	if bp.Hits != 0 {
		t.Errorf("disabled watch counted hits: %d", bp.Hits)
	}
}

func TestWBus_HotPathSkipWhenEmpty(t *testing.T) {
	// Sanity: with no watches installed, processMemHits returns no pause
	// and Drain returns no hits even after many accesses.
	m, _ := setup(t, progLoopWrites)
	// Note: m.MemBPs is empty.

	paused, _, _ := runUntilBRK(t, m, 200)
	if paused {
		t.Fatalf("no watches: must never pause")
	}
	if got := m.WBus.Drain(); len(got) != 0 {
		t.Errorf("drained %d hits with empty watches; want 0", len(got))
	}
}
