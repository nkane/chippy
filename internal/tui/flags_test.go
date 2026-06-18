package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

// TestSyncFlags_FromDAP proves the Flags panel snapshot is sourced through the
// DAP Flags scope (issue #450): New seeds m.Flags via the in-process server,
// which decomposes the live P register into bits — the panel never bit-tests
// cpu.CPU.P itself.
func TestSyncFlags_FromDAP(t *testing.T) {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	c.P = cpu.FlagU | cpu.FlagI | cpu.FlagC // 0x25

	m := New(c, ram)

	got := m.Flags
	want := FlagsSnapshot{U: true, I: true, C: true}
	if got != want {
		t.Fatalf("flags via DAP: want %+v, got %+v", want, got)
	}
}

func TestFlagsFromP(t *testing.T) {
	// All bits set -> every field true.
	all := flagsFromP(0xFF)
	if all != (FlagsSnapshot{true, true, true, true, true, true, true, true}) {
		t.Fatalf("flagsFromP(0xFF) should set every bit, got %+v", all)
	}
	// N and Z only.
	nz := flagsFromP(cpu.FlagN | cpu.FlagZ)
	if !nz.N || !nz.Z || nz.C || nz.V || nz.U || nz.B || nz.D || nz.I {
		t.Fatalf("flagsFromP(N|Z) should set only N and Z, got %+v", nz)
	}
}
