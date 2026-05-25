package cpu

// StackCodeMinAddr is the lowest address treated as plausibly executable
// code by DetectStackFrame. Real programs don't put JSR opcodes or JSR
// targets in the zero page ($0000-$00FF) or stack page ($0100-$01FF), so
// any candidate frame whose stored return-address or call target falls
// below this bar is rejected.
const StackCodeMinAddr uint16 = 0x0200

// DetectStackFrame reports whether the byte pair at $01XX (spLo, spLo+1)
// in RAM looks like a JSR-pushed return address. Used by the TUI's
// annotated stack panel and the DAP `stackTrace` request to walk the
// 6502 call chain. The heuristic checks three signals; all must hold:
//
//  1. The byte two below the stored 16-bit value is the JSR opcode ($20).
//  2. The stored return address points at executable space
//     (≥ StackCodeMinAddr).
//  3. The JSR's call target (read from the two operand bytes that follow
//     the opcode) also points at executable space.
//
// retAddr is `stored + 1` (the address RTS will jump to); target is the
// JSR's call target. False positives are still possible (misleading
// annotation, never a crash) — random stack bytes that happen to point
// one past a $20 in real code can register as frames.
func DetectStackFrame(ram *RAM, spLo uint16) (retAddr, target uint16, ok bool) {
	if (spLo & 0xFF) == 0xFF {
		return 0, 0, false
	}
	lo := ram.Read(spLo)
	hi := ram.Read(spLo + 1)
	stored := uint16(hi)<<8 | uint16(lo)
	if stored < StackCodeMinAddr {
		return 0, 0, false
	}
	if ram.Read(stored-2) != 0x20 {
		return 0, 0, false
	}
	targetLo := ram.Read(stored - 1)
	targetHi := ram.Read(stored)
	target = uint16(targetHi)<<8 | uint16(targetLo)
	if target < StackCodeMinAddr {
		return 0, 0, false
	}
	return stored + 1, target, true
}
