package cpu_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/loader"
)

// TestCMOSDemo_E2E loads the ca65-built example/cmos_demo.bin and runs it
// under the CMOS variant. It verifies CPU end-state matches what the
// program is supposed to produce: A=$11, X=$BB, Y=$CC, two stack pushes
// ($CC then $BB top), zero written to $0000, and the CPU halted in the
// JMP-self spin loop.
func TestCMOSDemo_E2E(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("can't locate test file")
	}
	bin := filepath.Join(filepath.Dir(thisFile), "..", "..", "example", "cmos_demo.bin")

	ram := cpu.NewRAM()
	if _, err := loader.Load(ram, bin, loader.Options{Addr: 0x8000}); err != nil {
		// CI sets CHIPPY_CMOS_E2E_STRICT=1 so a missing fixture fails
		// the build instead of silently skipping. Local devs run
		// `make -C example cmos_demo.bin` once and it Just Works.
		if os.Getenv("CHIPPY_CMOS_E2E_STRICT") != "" {
			t.Fatalf("cmos_demo.bin missing under strict mode (CI must build it before running this test): %v", err)
		}
		t.Skipf("cmos_demo.bin not built (run `make -C example cmos_demo.bin`): %v", err)
	}

	c := cpu.NewVariant(ram, cpu.VariantCMOS65C02)
	for i := 0; i < 50 && !c.Halted; i++ {
		c.Step()
	}
	if !c.Halted {
		t.Fatalf("program never halted; PC=%04X A=%02X X=%02X Y=%02X", c.PC, c.A, c.X, c.Y)
	}
	if c.A != 0x11 {
		t.Fatalf("A=%02X want 11", c.A)
	}
	if c.X != 0xBB {
		t.Fatalf("X=%02X want BB", c.X)
	}
	if c.Y != 0xCC {
		t.Fatalf("Y=%02X want CC", c.Y)
	}
	if ram.Read(0x0000) != 0 {
		t.Fatalf("$0000=%02X want 00 (STZ failed)", ram.Read(0x0000))
	}
	// Stack grows down from $01FD; first push (PHX, $BB) at $01FD,
	// second push (PHY, $CC) at $01FC.
	if ram.Read(0x01FD) != 0xBB {
		t.Fatalf("$01FD=%02X want BB (PHX)", ram.Read(0x01FD))
	}
	if ram.Read(0x01FC) != 0xCC {
		t.Fatalf("$01FC=%02X want CC (PHY)", ram.Read(0x01FC))
	}
}
