package cpu

import "testing"

func TestAccessHook_Classifies(t *testing.T) {
	ram := NewRAM()
	// $8000: LDA #$42   (A9 42)
	// $8002: STA $0200  (8D 00 02)
	ram.Load(0x8000, []byte{0xA9, 0x42, 0x8D, 0x00, 0x02})
	c := New(ram)
	c.PC = 0x8000

	type acc struct {
		addr uint16
		kind AccessKind
	}
	var log []acc
	c.SetAccessHook(func(addr uint16, kind AccessKind) {
		log = append(log, acc{addr, kind})
	})

	c.Step() // LDA #$42
	c.Step() // STA $0200

	// Opcode bytes are exec; operand bytes are read; the store is a write.
	want := map[uint16]AccessKind{
		0x8000: AccessExec,  // LDA opcode
		0x8001: AccessRead,  // immediate operand
		0x8002: AccessExec,  // STA opcode
		0x8003: AccessRead,  // addr low
		0x8004: AccessRead,  // addr high
		0x0200: AccessWrite, // store target
	}
	seen := map[uint16]AccessKind{}
	for _, a := range log {
		seen[a.addr] = a.kind
	}
	for addr, kind := range want {
		if got, ok := seen[addr]; !ok || got != kind {
			t.Errorf("access $%04X = %v (present=%v); want %v", addr, got, ok, kind)
		}
	}
}

func TestAccessHook_Disabled(t *testing.T) {
	ram := NewRAM()
	ram.Load(0x8000, []byte{0xEA}) // NOP
	c := New(ram)
	c.PC = 0x8000
	n := 0
	c.SetAccessHook(func(uint16, AccessKind) { n++ })
	c.Step()
	if n == 0 {
		t.Fatal("hook never fired")
	}
	c.SetAccessHook(nil)
	before := n
	c.PC = 0x8000
	c.Step()
	if n != before {
		t.Errorf("hook fired after being cleared: %d -> %d", before, n)
	}
}

func TestAccessKind_String(t *testing.T) {
	for k, s := range map[AccessKind]string{AccessRead: "read", AccessWrite: "write", AccessExec: "exec"} {
		if k.String() != s {
			t.Errorf("%d.String() = %q; want %q", k, k.String(), s)
		}
	}
}
