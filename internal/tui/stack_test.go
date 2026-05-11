package tui

import (
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

func TestDetectStackFrame_Positive(t *testing.T) {
	ram := cpu.NewRAM()
	// Caller routine at $8000: JSR $9000
	//   $8000  20 00 90    JSR $9000
	//   $8003  ...         (return target)
	ram.Write(0x8000, 0x20) // JSR opcode
	ram.Write(0x8001, 0x00) // operand lo
	ram.Write(0x8002, 0x90) // operand hi

	// Simulate the stack state after JSR has executed: push16(PC-1) with
	// PC = $8003 means stored value $8002 with hi=$80 at $01FF and lo=$02
	// at $01FE.
	ram.Write(0x01FE, 0x02) // lo of stored ($8002)
	ram.Write(0x01FF, 0x80) // hi

	ret, target, ok := detectStackFrame(ram, 0x01FE)
	if !ok {
		t.Fatalf("expected frame to be detected")
	}
	if ret != 0x8003 {
		t.Fatalf("want ret $8003, got $%04X", ret)
	}
	if target != 0x9000 {
		t.Fatalf("want target $9000, got $%04X", target)
	}
}

func TestDetectStackFrame_NotAJSR(t *testing.T) {
	// Same stored value but the byte at stored-2 isn't $20 — should be
	// rejected as a false candidate.
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0xEA) // NOP, not JSR
	ram.Write(0x01FE, 0x02)
	ram.Write(0x01FF, 0x80)

	if _, _, ok := detectStackFrame(ram, 0x01FE); ok {
		t.Fatalf("should not have detected a frame without a JSR opcode")
	}
}

func TestDetectStackFrame_TopOfPage(t *testing.T) {
	// The high byte of the pair would live at $0200 (off the stack page).
	// Detection must refuse so the panel renderer never reaches outside
	// the stack window.
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0x20)
	ram.Write(0x01FF, 0x02)
	ram.Write(0x0200, 0x80) // would-be hi byte — outside stack page

	if _, _, ok := detectStackFrame(ram, 0x01FF); ok {
		t.Fatalf("should refuse the very last stack slot — no pair available")
	}
}

func TestDetectStackFrame_StoredTooLow(t *testing.T) {
	// Stored value < 2 means there's no room for a JSR opcode below it.
	ram := cpu.NewRAM()
	ram.Write(0x01FE, 0x01)
	ram.Write(0x01FF, 0x00)
	if _, _, ok := detectStackFrame(ram, 0x01FE); ok {
		t.Fatalf("stored=$0001 has no opcode-2 byte; should be rejected")
	}
}

func TestStackEntries_FrameAndRun(t *testing.T) {
	// Build a stack with two frames separated by three non-frame bytes:
	//   $01FE-FF : frame ret $8003 (JSR at $8000)
	//   $01FD    : random byte
	//   $01FC    : random byte
	//   $01FB    : random byte
	//   $01F9-FA : frame ret $7003 (JSR at $7000)
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0x20)
	ram.Write(0x8001, 0x00)
	ram.Write(0x8002, 0x90)
	ram.Write(0x7000, 0x20)
	ram.Write(0x7001, 0x34)
	ram.Write(0x7002, 0x12)

	// Top frame at $01FE/FF -> stored=$8002
	ram.Write(0x01FE, 0x02)
	ram.Write(0x01FF, 0x80)
	// Random in-between bytes
	ram.Write(0x01FB, 0xAA)
	ram.Write(0x01FC, 0xBB)
	ram.Write(0x01FD, 0xCC)
	// Lower frame at $01F9/FA -> stored=$7002
	ram.Write(0x01F9, 0x02)
	ram.Write(0x01FA, 0x70)

	c := cpu.New(ram)
	c.SP = 0xF8 // SP+1 = $F9 → walk from $01F9

	m := New(c, ram)
	entries := m.stackEntries(10)

	if len(entries) != 3 {
		t.Fatalf("want 3 entries (frame, run-of-3, frame), got %d", len(entries))
	}
	if !entries[0].isFrame || entries[0].retAddr != 0x7003 {
		t.Fatalf("entry 0: want frame ret $7003, got %+v", entries[0])
	}
	if entries[1].isFrame || entries[1].bytes != 3 || entries[1].addrLo != 0x01FB {
		t.Fatalf("entry 1: want 3-byte run at $01FB, got %+v", entries[1])
	}
	if !entries[2].isFrame || entries[2].retAddr != 0x8003 {
		t.Fatalf("entry 2: want frame ret $8003, got %+v", entries[2])
	}
}

func TestStackEntries_DisabledAnnotation(t *testing.T) {
	// With StackAnnotate=false the walker should collapse the entire visible
	// region into a single run (frame detection is skipped). Renderer takes
	// the legacy path; we still exercise stackEntries directly to confirm
	// the flag is honored.
	ram := cpu.NewRAM()
	ram.Write(0x8000, 0x20)
	ram.Write(0x01FE, 0x02)
	ram.Write(0x01FF, 0x80)

	c := cpu.New(ram)
	c.SP = 0xFD

	m := New(c, ram)
	m.StackAnnotate = false
	entries := m.stackEntries(4)

	for i, e := range entries {
		if e.isFrame {
			t.Fatalf("entry %d should not be a frame when annotation is disabled: %+v", i, e)
		}
	}
}
