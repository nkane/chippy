//go:build accuracy

// Phase probe for cpu_interrupts_v2 (#372). Runs the ROM under the
// production NES build and writes a per-instruction trace covering the
// CPU cycle, PPU frame/scanline/dot, and opcode bytes — formatted to
// align with a Mesen2 trace log so the two can be diffed by PC sequence
// to pin down the PPU phase offset that breaks test 3 calibration.
//
// Gate via env so the regular accuracy suite stays cheap:
//
//	CHIPPY_PHASE_PROBE_OUT=/tmp/chippy_probe.txt \
//	CHIPPY_PHASE_PROBE_CYCLES=300000 \
//	  go test -tags=accuracy -run TestCPUInterruptsPhaseProbe -v ./cmd/nessy/...
//
// Default cycle cap is 250000 (~8 NTSC frames) — enough for ROM reset,
// initial sync_vbl, and the early test scaffolding. Bump via env if a
// deeper window is needed.
package main

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"testing"
	"unsafe"

	"github.com/nkane/chippy/internal/nes"
)

// nmiInternal pries the unexported NMI poll state out of cpu.CPU so the
// probe can log it side-by-side with the visible PC/cycle/PPU phase. The
// fields are accessed by name via reflect so we don't depend on field
// order, and unsafe.Pointer lets us read them despite the package
// boundary. Test-only — never link this into a non-probe binary.
type nmiInternal struct {
	nmiPending, nmiDue, nmiPollPrev, nmiLineLevel bool
	irqDue, irqPollPrev                           bool
}

func readNMIInternal(c interface{}) nmiInternal {
	v := reflect.ValueOf(c).Elem()
	get := func(name string) bool {
		f := v.FieldByName(name)
		return *(*bool)(unsafe.Pointer(f.UnsafeAddr()))
	}
	return nmiInternal{
		nmiPending:   get("nmiPending"),
		nmiDue:       get("nmiDue"),
		nmiPollPrev:  get("nmiPollPrev"),
		nmiLineLevel: get("nmiLineLevel"),
		irqDue:       get("irqDue"),
		irqPollPrev:  get("irqPollPrev"),
	}
}

func TestCPUInterruptsPhaseProbe(t *testing.T) {
	out := os.Getenv("CHIPPY_PHASE_PROBE_OUT")
	if out == "" {
		t.Skip("set CHIPPY_PHASE_PROBE_OUT to enable phase probe")
	}
	maxCycles := uint64(250000)
	if s := os.Getenv("CHIPPY_PHASE_PROBE_CYCLES"); s != "" {
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			t.Fatalf("CHIPPY_PHASE_PROBE_CYCLES: %v", err)
		}
		maxCycles = v
	}

	var rom accuracyROM
	for _, r := range accuracyROMs {
		if r.name == "cpu_interrupts_v2.nes" {
			rom = r
			break
		}
	}
	data, err := loadAccuracyROM(t, rom)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	parsed, err := nes.ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	bus, err := buildNES(parsed)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	f, err := os.Create(out)
	if err != nil {
		t.Fatalf("create %s: %v", out, err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()

	// Header documents the column layout so the file is self-describing
	// when grepped or diffed in isolation.
	fmt.Fprintf(w, "# cpu_interrupts_v2 phase probe — chippy model trace (#372)\n")
	fmt.Fprintf(w, "# cols: PC  cpu_cyc  frame  sl  dot  b0 b1 b2  A X Y P SP  nmi=L/P/PP/D/Pend irq=D/PP\n")

	for bus.cpu.Cycles < maxCycles && !bus.cpu.Halted {
		pc := bus.cpu.PC
		b0 := bus.cart.CPURead(pc)
		b1 := bus.cart.CPURead(pc + 1)
		b2 := bus.cart.CPURead(pc + 2)
		nm := readNMIInternal(bus.cpu)
		fmt.Fprintf(w,
			"%04X  %d  f=%d  sl=%d  dot=%d  %02X %02X %02X  A=%02X X=%02X Y=%02X P=%02X SP=%02X  nmi=%d%d%d%d%d irq=%d%d apuC=%d\n",
			pc, bus.cpu.Cycles, bus.ppu.FrameCount(), bus.ppu.Scanline(), bus.ppu.Dot(),
			b0, b1, b2,
			bus.cpu.A, bus.cpu.X, bus.cpu.Y, bus.cpu.P, bus.cpu.SP,
			b2i(nm.nmiLineLevel), b2i(nm.nmiPending), b2i(nm.nmiPollPrev), b2i(nm.nmiDue), b2i(nm.nmiPending),
			b2i(nm.irqDue), b2i(nm.irqPollPrev), bus.apu.DbgAPUCycles())
		bus.cpu.Step()
	}

	// Dump APU IRQ assert cycles at end for offline diff vs Mesen.
	if asserts := bus.apu.DbgIRQAsserts(); len(asserts) > 0 {
		fmt.Fprintf(w, "\n# APU frame-counter IRQ assert APU-cycle log:\n")
		for i, c := range asserts {
			fmt.Fprintf(w, "# irq #%d apuC=%d\n", i, c)
		}
	}
	if resets := bus.apu.DbgFrameResets(); len(resets) > 0 {
		fmt.Fprintf(w, "\n# $4017 reset log (apuC, delay, alternateTick):\n")
		for i, e := range resets {
			fmt.Fprintf(w, "# rst #%d apuC=%d delay=%d alt=%d\n", i, e[0], e[1], e[2])
		}
	}
	t.Logf("phase probe → %s (cycles<%d, halted=%v, irqs=%d)",
		out, maxCycles, bus.cpu.Halted, len(bus.apu.DbgIRQAsserts()))
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
