package cpu_test

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

// TestDisasm_NMOSDefault confirms the legacy Disasm function still routes
// through the NMOS table.
func TestDisasm_NMOSDefault(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0xA9) // LDA #imm
	ram.Write(0x8001, 0x42)
	got, n := cpu.Disasm(ram, 0x8000)
	if n != 2 {
		t.Fatalf("byte count: want 2, got %d", n)
	}
	if !strings.Contains(got, "LDA") || !strings.Contains(got, "#$42") {
		t.Fatalf("LDA #$42 disasm: got %q", got)
	}
}

// TestDisasm_CMOSOnlyOpcodeOnNMOS shows the failure mode this PR fixes:
// Disasm()'s fixed NMOS table can't decode CMOS-only opcodes correctly. We
// don't assert any particular wrong output — just demonstrate that without
// variant dispatch the result differs from the CMOS-aware path below.
func TestDisasm_CMOSOnlyOpcodeOnNMOS(t *testing.T) {
	ram := cpu.NewRAM()
	// $80 is BRA on CMOS, an illegal NOP on NMOS.
	ram.Write(0x8000, 0x80)
	ram.Write(0x8001, 0x10)
	nmosOut, _ := cpu.Disasm(ram, 0x8000)
	if strings.Contains(nmosOut, "BRA") {
		t.Fatalf("legacy Disasm should NOT decode BRA (NMOS-only path); got %q", nmosOut)
	}
}

// TestDisasmCPU_CMOS demonstrates the fix: a CPU constructed with the CMOS
// variant disassembles its own opcodes correctly.
func TestDisasmCPU_CMOS(t *testing.T) {
	ram := cpu.NewRAM()
	// CMOS opcodes that don't exist on NMOS:
	//   $80 rel   BRA
	//   $DA       PHX
	//   $5A       PHY
	//   $64 zp    STZ
	//   $9C abs   STZ
	ram.Write(0x8000, 0x80) // BRA $8004
	ram.Write(0x8001, 0x02)
	ram.Write(0x8002, 0xDA) // PHX
	ram.Write(0x8003, 0x5A) // PHY
	ram.Write(0x8004, 0x64) // STZ $42
	ram.Write(0x8005, 0x42)
	ram.Write(0x8006, 0x9C) // STZ $0200
	ram.Write(0x8007, 0x00)
	ram.Write(0x8008, 0x02)
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)

	c := cpu.NewVariant(ram, cpu.VariantCMOS65C02)

	cases := []struct {
		addr uint16
		want string
		size int
	}{
		{0x8000, "BRA ", 2},
		{0x8002, "PHX", 1},
		{0x8003, "PHY", 1},
		{0x8004, "STZ ", 2},
		{0x8006, "STZ ", 3},
	}
	for _, tc := range cases {
		got, n := cpu.DisasmCPU(c, tc.addr)
		if !strings.Contains(got, tc.want) {
			t.Errorf("$%04X CMOS disasm: want substring %q, got %q", tc.addr, tc.want, got)
		}
		if n != tc.size {
			t.Errorf("$%04X size: want %d, got %d", tc.addr, tc.size, n)
		}
	}
}

// TestDisasmCPU_NMOSPreservesLegacyBehavior ensures the new CPU-aware API
// produces the same output as the legacy API when the CPU happens to be
// NMOS — the path through disasmWithTable is identical.
func TestDisasmCPU_NMOSPreservesLegacyBehavior(t *testing.T) {
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0xA9)
	ram.Write(0x8001, 0x42)
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)
	c := cpu.New(ram) // NMOS default

	legacy, n1 := cpu.Disasm(ram, 0x8000)
	cpuOut, n2 := cpu.DisasmCPU(c, 0x8000)
	if legacy != cpuOut {
		t.Fatalf("NMOS legacy vs CPU disagree:\n legacy: %q\n cpu:    %q", legacy, cpuOut)
	}
	if n1 != n2 {
		t.Fatalf("size disagreement: legacy=%d cpu=%d", n1, n2)
	}
}
