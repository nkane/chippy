//go:build lorenz

// Wolfgang Lorenz's C64 test suite (testsuite-2.15) is the gold standard for
// 6510/6502 instruction, decimal, and flag-edge behaviour. Each test is a C64
// .prg that drives a tiny KERNAL-trap harness — no real C64 bus needed for the
// pure-CPU subset.
//
// chippy vendors the CPU-only subset (cpu/testdata/lorenz/) — the opcode,
// decimal, and stable-illegal probes (including the decimal-mode ARR probe
// `arrb`, fixed in #424). The C64-hardware tests (CIA/SID/VIC timers, NMI/IRQ
// sourcing, banking) are out of scope; Klaus's interrupt ROM
// (interrupt_rom_test.go) covers IRQ/NMI/BRK.
//
// Harness (per http://www.softwolves.com/arkiv/cbm-hackers/7/7114.html, and
// floooh/chips-test m6502-wltest.c): load the dump at its 2-byte header
// address, seed a handful of RAM cells + a KERNAL IRQ shim at $FF48, enter at
// $0801, and trap KERNAL entry points:
//
//	$FFD2 CHROUT  — print a PETSCII char (test progress / diagnostics), RTS
//	$E16F LOAD    — the test passed and is chaining to the next: report PASS
//	$FFE4 GETIN   — reached only after a failure message: report FAIL
//	$8000/$A474   — done
//
// Each subtest is run standalone (its dump self-initialises at $0801), so a
// failure names the exact probe.

package cpu

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// lorenzKernalIRQ is the minimal KERNAL IRQ/BRK dispatcher the suite expects
// at $FF48: save regs, then branch on the stacked B flag to ($0316) for BRK or
// ($0314) for IRQ.
var lorenzKernalIRQ = []byte{
	0x48,       // PHA
	0x8A, 0x48, // TXA, PHA
	0x98, 0x48, // TYA, PHA
	0xBA,             // TSX
	0xBD, 0x04, 0x01, // LDA $0104,X
	0x29, 0x10, // AND #$10
	0xF0, 0x03, // BEQ +3
	0x6C, 0x16, 0x03, // JMP ($0316)
	0x6C, 0x14, 0x03, // JMP ($0314)
}

// lorenzMaxInstr bounds a single subtest. The slowest probes sweep ~16M
// operand combinations at a few instructions each.
const lorenzMaxInstr = 120_000_000

func TestLorenzSuite(t *testing.T) {
	dir := filepath.Join("testdata", "lorenz")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("lorenz testdata unavailable: %v", err)
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Skip("no lorenz test dumps vendored")
	}

	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || len(data) < 3 {
				t.Fatalf("read %s: %v", name, err)
			}
			ok, out := runLorenzTest(data)
			if !ok {
				t.Fatalf("%s failed:\n%s", name, out)
			}
		})
	}
}

// runLorenzTest loads one dump, runs it standalone, and returns (passed,
// captured PETSCII output). A test passes when it chains to the next ($E16F)
// or exits ($8000/$A474); it fails when it falls into the error keyscan
// ($FFE4) or runs away.
func runLorenzTest(dump []byte) (bool, string) {
	addr := uint16(dump[0]) | uint16(dump[1])<<8
	ram := NewRAM()
	ram.Load(addr, dump[2:])

	// Suite environment seed.
	ram.Write(0x0002, 0x00)
	ram.Write(0xA002, 0x00)
	ram.Write(0xA003, 0x80)
	ram.Write(0xFFFE, 0x48) // IRQ/BRK vector -> $FF48
	ram.Write(0xFFFF, 0xFF)
	ram.Write(0x01FE, 0xFF)
	ram.Write(0x01FF, 0x7F)
	ram.Load(0xFF48, lorenzKernalIRQ)

	c := New(ram) // VariantNMOS
	c.Reset()
	c.SP = 0xFD
	c.P = FlagB | FlagI | FlagU
	c.PC = 0x0801

	var out strings.Builder
	pop := func() uint16 {
		lo := ram.Read(0x0100 | uint16(c.SP+1))
		hi := ram.Read(0x0100 | uint16(c.SP+2))
		c.SP += 2
		return uint16(hi)<<8 | uint16(lo)
	}

	for i := 0; i < lorenzMaxInstr; i++ {
		switch c.PC {
		case 0xFFD2: // CHROUT: print A, return
			ram.Write(0x030C, 0x00)
			out.WriteByte(lorenzPetscii(c.A))
			c.PC = pop() + 1
			continue
		case 0xE16F: // chaining to next test -> passed
			return true, out.String()
		case 0xFFE4: // error keyscan -> failed
			return false, strings.TrimSpace(out.String())
		case 0x8000, 0xA474: // done
			return true, out.String()
		}
		c.Step()
	}
	return false, "timeout\n" + strings.TrimSpace(out.String())
}

// lorenzPetscii converts the PETSCII the suite prints into ASCII for readable
// failure diagnostics.
func lorenzPetscii(p byte) byte {
	switch {
	case p < 0x20:
		if p == 0x0D {
			return '\n'
		}
		return ' '
	case p >= 0x41 && p <= 0x5A:
		return 'a' + (p - 0x41)
	case p >= 0xC1 && p <= 0xDA:
		return 'A' + (p - 0xC1)
	case p < 0x80:
		return p
	default:
		return ' '
	}
}
