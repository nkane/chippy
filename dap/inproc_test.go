package dap

import (
	"testing"
	"time"

	"github.com/nkane/chippy/cpu"
)

// inprocSession spins up an in-process server attached to a CPU running a NOP
// sled at $8000, and returns the client plus the CPU for direct assertions.
func inprocSession(t *testing.T) (*InprocClient, *cpu.CPU) {
	t.Helper()
	ram := cpu.NewRAM()
	for a := 0x8000; a < 0x9000; a++ {
		ram.Write(uint16(a), 0xEA) // NOP
	}
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()
	srv, cl := NewInprocServer()
	if err := srv.AttachExisting(AttachConfig{CPU: c, RAM: ram}); err != nil {
		t.Fatalf("AttachExisting: %v", err)
	}
	return cl, c
}

func TestInproc_Handshake(t *testing.T) {
	cl, _ := inprocSession(t)

	resp, err := cl.Initialize()
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if !resp.Success {
		t.Fatalf("initialize success=false: %s", resp.Message)
	}
	// initialize emits an `initialized` event after the response.
	select {
	case ev := <-cl.Events():
		if ev.Event != "initialized" {
			t.Errorf("first event = %q; want initialized", ev.Event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no initialized event")
	}

	if resp, err := cl.Attach(); err != nil || !resp.Success {
		t.Fatalf("attach: %v / %+v", err, resp)
	}
}

func TestInproc_StepAdvancesPC(t *testing.T) {
	cl, c := inprocSession(t)
	if _, err := cl.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Attach(); err != nil {
		t.Fatal(err)
	}

	pc0 := c.PC
	if _, err := cl.Request("stepIn", nil); err != nil {
		t.Fatalf("stepIn: %v", err)
	}
	if c.PC != pc0+1 {
		t.Errorf("PC after stepIn = $%04X; want $%04X (one NOP past $%04X)", c.PC, pc0+1, pc0)
	}
}

// TestInproc_TypedArgsRoundTrip exercises a request carrying typed args (one
// marshal) and a structured response body delivered without marshalling.
func TestInproc_TypedArgsRoundTrip(t *testing.T) {
	cl, _ := inprocSession(t)
	if _, err := cl.Initialize(); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Attach(); err != nil {
		t.Fatal(err)
	}
	resp, err := cl.Request("variables", VariablesArguments{VariablesReference: refRegisters})
	if err != nil || !resp.Success {
		t.Fatalf("variables: %v / %+v", err, resp)
	}
	if resp.Body == nil {
		t.Fatal("variables response body is nil")
	}
}

func TestInproc_DisconnectEndsSession(t *testing.T) {
	cl, _ := inprocSession(t)
	if _, err := cl.Initialize(); err != nil {
		t.Fatal(err)
	}
	if resp, err := cl.Disconnect(); err != nil || !resp.Success {
		t.Fatalf("disconnect: %v / %+v", err, resp)
	}
}

// BenchmarkInprocStepIn measures the in-process round-trip for a stepIn (nil
// args -> zero serialization in, struct sink out).
func BenchmarkInprocStepIn(b *testing.B) {
	ram := cpu.NewRAM()
	for a := 0x8000; a < 0x9000; a++ {
		ram.Write(uint16(a), 0xEA)
	}
	ram.Write(0xFFFC, 0x00)
	ram.Write(0xFFFD, 0x80)
	c := cpu.New(ram)
	c.Reset()
	srv, cl := NewInprocServer()
	_ = srv.AttachExisting(AttachConfig{CPU: c, RAM: ram})
	_, _ = cl.Initialize()
	_, _ = cl.Attach()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if c.PC >= 0x8F00 {
			c.PC = 0x8000
		}
		if _, err := cl.Request("stepIn", nil); err != nil {
			b.Fatal(err)
		}
	}
}
