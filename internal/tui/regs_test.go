package tui

import (
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

func newRegsModel(t *testing.T) (Model, *cpu.CPU) {
	t.Helper()
	ram := cpu.NewRAM()
	for a := 0x8000; a < 0x8100; a++ {
		ram.Write(uint16(a), 0xEA) // NOP sled
	}
	c := cpu.New(ram)
	c.PC, c.A, c.X, c.Y, c.SP = 0x8000, 0x42, 0x13, 0x99, 0xF0
	return New(c, ram), c
}

// New seeds m.Regs via a DAP variables round-trip (LocalSource inproc).
func TestRegs_SeededFromDAP(t *testing.T) {
	m, _ := newRegsModel(t)
	if m.Regs.A != 0x42 || m.Regs.X != 0x13 || m.Regs.Y != 0x99 ||
		m.Regs.SP != 0xF0 || m.Regs.PC != 0x8000 {
		t.Fatalf("seeded regs = %+v; want A=42 X=13 Y=99 SP=F0 PC=8000", m.Regs)
	}
}

func TestRegs_FollowStep(t *testing.T) {
	m, _ := newRegsModel(t)
	m.step()
	m.syncRegs()
	if m.Regs.PC != 0x8001 {
		t.Errorf("PC after one NOP = $%04X; want $8001", m.Regs.PC)
	}
}

// regsView renders from the snapshot, not cpu.CPU.
func TestRegs_ViewRendersSnapshot(t *testing.T) {
	m, _ := newRegsModel(t)
	out := m.regsView(40, 6)
	for _, want := range []string{"$42", "$13", "$99", "$F0", "$8000"} {
		if !strings.Contains(out, want) {
			t.Errorf("regsView missing %q:\n%s", want, out)
		}
	}
}

// LocalSource.Registers returns live state through the in-process DAP server.
func TestLocalSource_RegistersViaInproc(t *testing.T) {
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	c.A, c.PC, c.Cycles = 0xAB, 0x1234, 99
	s := NewLocalSource(c, ram)
	rs, err := s.Registers()
	if err != nil {
		t.Fatalf("Registers: %v", err)
	}
	if rs.A != 0xAB || rs.PC != 0x1234 || rs.Cycles != 99 {
		t.Errorf("snapshot = %+v; want A=AB PC=1234 Cycles=99", rs)
	}
}
