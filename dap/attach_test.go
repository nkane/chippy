package dap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

func TestAttach_AttachExistingRefusesWhenAlreadyAttached(t *testing.T) {
	s, _, _ := newStoppedServer(t, []byte{0xEA})
	// newStoppedServer already wired cpu+ram; AttachExisting should refuse.
	err := s.AttachExisting(AttachConfig{
		CPU: cpu.New(cpu.NewRAM()),
		RAM: cpu.NewRAM(),
	})
	if err == nil {
		t.Fatalf("AttachExisting should refuse when a debuggee is already wired")
	}
	if !strings.Contains(err.Error(), "already attached") {
		t.Fatalf("expected 'already attached' in error, got: %v", err)
	}
}

func TestAttach_AttachExistingRequiresCPUAndRAM(t *testing.T) {
	var s Server
	err := s.AttachExisting(AttachConfig{})
	if err == nil {
		t.Fatalf("empty config should error")
	}
	if !strings.Contains(err.Error(), "CPU and RAM") {
		t.Fatalf("expected error to call out CPU/RAM requirement, got: %v", err)
	}
}

func TestAttach_HandleAttachEmitsStopped(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA, 0xEA})

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "attach",
		Arguments:       json.RawMessage(`{"stopOnEntry":true}`),
	}
	s.handleAttach(req)

	body := out.String()
	if !strings.Contains(body, `"command":"attach"`) || !strings.Contains(body, `"success":true`) {
		t.Fatalf("attach should succeed when debuggee is wired, got: %s", body)
	}
	if !strings.Contains(body, `"event":"stopped"`) {
		t.Fatalf("attach should emit stopped event, got: %s", body)
	}
	if !strings.Contains(body, `"reason":"entry"`) {
		t.Fatalf("stopped reason should be entry, got: %s", body)
	}
}

func TestAttach_AttachExistingPopulatesAllFields(t *testing.T) {
	// Build everything externally — what a host process like the TUI
	// would hand to NewServer + AttachExisting.
	ram := cpu.NewRAM()
	mmio := cpu.NewMMIO(ram)
	c := cpu.New(mmio)

	var s Server
	if err := s.AttachExisting(AttachConfig{
		CPU:  c,
		RAM:  ram,
		MMIO: mmio,
	}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	if s.cpu != c {
		t.Fatalf("server.cpu should be the supplied CPU")
	}
	if s.ram != ram {
		t.Fatalf("server.ram should be the supplied RAM")
	}
	if s.mmio != mmio {
		t.Fatalf("server.mmio should be the supplied MMIO")
	}
}
