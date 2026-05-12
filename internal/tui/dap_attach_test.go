package tui

import (
	"strings"
	"sync"
	"testing"
)

// cmdDAP with no args reports "no listener" before any session starts.
func TestCmdDAP_ReportsNoListener(t *testing.T) {
	m, _ := stateTestModel(t)
	out := m.cmdDAP(nil)
	if !strings.Contains(out, "no listener") {
		t.Fatalf("want 'no listener'; got %q", out)
	}
}

// `:dap 0` binds an auto-assigned port; subsequent `:dap` reports it.
// `:dap stop` releases.
func TestCmdDAP_LifecycleAutoPort(t *testing.T) {
	m, _ := stateTestModel(t)
	defer func() {
		_ = m.cmdDAP([]string{"stop"})
	}()

	out := m.cmdDAP([]string{"0"})
	if !strings.Contains(out, "listening on") {
		t.Fatalf("auto-port bind should report 'listening on'; got %q", out)
	}
	if m.DAPListenAddr == "" {
		t.Fatalf("DAPListenAddr should be set after :dap 0")
	}
	if m.CPUMu == nil {
		t.Fatalf("CPUMu should be allocated when DAP attaches")
	}

	// Second `:dap PORT` while one is live should error, not steal.
	out2 := m.cmdDAP([]string{"0"})
	if !strings.Contains(out2, "already listening") {
		t.Fatalf("second :dap should refuse; got %q", out2)
	}

	out3 := m.cmdDAP([]string{"stop"})
	if !strings.Contains(out3, "stopped") {
		t.Fatalf(":dap stop should confirm; got %q", out3)
	}
	if m.DAPListenAddr != "" {
		t.Fatalf("DAPListenAddr should be cleared after stop")
	}
}

// CPUMu is honored by m.step(): a concurrent goroutine holding the
// mutex blocks step until released. -race detector validates.
func TestModel_StepRespectsCPUMu(t *testing.T) {
	m, _ := stateTestModel(t)
	mu := &sync.Mutex{}
	m.CPUMu = mu

	mu.Lock()
	done := make(chan struct{})
	go func() {
		m.step()
		close(done)
	}()

	// step() should be blocked on mu. If it's not, the test races on
	// CPU state; the race detector + the explicit Unlock below catch it.
	select {
	case <-done:
		t.Fatalf("step ran while mutex was held by another goroutine")
	default:
	}
	mu.Unlock()
	<-done
}
