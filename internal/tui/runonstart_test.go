package tui

import (
	"testing"

	"github.com/nkane/chippy/cpu"
)

func TestWithRunOnStart_True(t *testing.T) {
	c := cpu.New(cpu.NewRAM())
	m := New(c, cpu.NewRAM()).WithRunOnStart(true)
	if !m.Running {
		t.Fatalf("Running should be true after WithRunOnStart(true)")
	}
	if m.Status != "running" {
		t.Fatalf("Status should reflect the running state, got %q", m.Status)
	}
}

func TestWithRunOnStart_False(t *testing.T) {
	c := cpu.New(cpu.NewRAM())
	m := New(c, cpu.NewRAM()).WithRunOnStart(false)
	if m.Running {
		t.Fatalf("Running should be false by default")
	}
	if m.Status != "ready" {
		t.Fatalf("Status should be unchanged from default, got %q", m.Status)
	}
}
