package cpu

import "testing"

func TestDetectStackFrame_Positive(t *testing.T) {
	ram := NewRAM()
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

	ret, target, ok := DetectStackFrame(ram, 0x01FE)
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
	ram := NewRAM()
	ram.Write(0x8000, 0xEA) // NOP, not JSR
	ram.Write(0x01FE, 0x02)
	ram.Write(0x01FF, 0x80)

	if _, _, ok := DetectStackFrame(ram, 0x01FE); ok {
		t.Fatalf("should not have detected a frame without a JSR opcode")
	}
}

func TestDetectStackFrame_TopOfPage(t *testing.T) {
	// The high byte of the pair would live at $0200 (off the stack page).
	// Detection must refuse so the panel renderer never reaches outside
	// the stack window.
	ram := NewRAM()
	ram.Write(0x8000, 0x20)
	ram.Write(0x01FF, 0x02)
	ram.Write(0x0200, 0x80) // would-be hi byte — outside stack page

	if _, _, ok := DetectStackFrame(ram, 0x01FF); ok {
		t.Fatalf("should refuse the very last stack slot — no pair available")
	}
}

func TestDetectStackFrame_StoredTooLow(t *testing.T) {
	// Stored value < StackCodeMinAddr means RAM the return points into is
	// zero-page or stack-page — degenerate, reject.
	ram := NewRAM()
	ram.Write(0x01FE, 0x01)
	ram.Write(0x01FF, 0x00)
	if _, _, ok := DetectStackFrame(ram, 0x01FE); ok {
		t.Fatalf("stored=$0001 has no opcode-2 byte; should be rejected")
	}
}

// TestDetectStackFrame_StoredInZeroPage exercises the StackCodeMinAddr gate. A
// stored value of $00FE has byte $20 placed at $00FC (so signal 1 passes),
// but the return falls in zero-page so the heuristic rejects it.
func TestDetectStackFrame_StoredInZeroPage(t *testing.T) {
	ram := NewRAM()
	ram.Write(0x00FC, 0x20) // looks like a JSR in zero-page
	ram.Write(0x01FE, 0xFE) // lo
	ram.Write(0x01FF, 0x00) // hi -> stored = $00FE (< $0200)
	if _, _, ok := DetectStackFrame(ram, 0x01FE); ok {
		t.Fatalf("stored in zero-page should be rejected by the StackCodeMinAddr gate")
	}
}

// TestDetectStackFrame_TargetInStackPage covers the third gate: stored looks
// fine, opcode is $20, but the JSR operand points at $01XX which is the stack
// page itself. Reject — real code doesn't JSR into the stack.
func TestDetectStackFrame_TargetInStackPage(t *testing.T) {
	ram := NewRAM()
	// JSR opcode at $7FFE, operand $00 $01 -> target $0100 (stack page).
	ram.Write(0x7FFE, 0x20)
	ram.Write(0x7FFF, 0x00)
	ram.Write(0x8000, 0x01)
	// stored value $8000 (signals 1+2 pass).
	ram.Write(0x01FE, 0x00)
	ram.Write(0x01FF, 0x80)
	if _, _, ok := DetectStackFrame(ram, 0x01FE); ok {
		t.Fatalf("JSR target in stack-page should be rejected")
	}
}
