package apu

import "testing"

// writeOPLL latches a register address + writes data via the cart's
// $9010/$9030 port pair.
func writeOPLL(v *VRC7Audio, reg, val byte) {
	v.Write(0x9010, reg)
	v.Write(0x9030, val)
}

func TestVRC7Audio_RegCapture(t *testing.T) {
	v := NewVRC7Audio()
	writeOPLL(v, 0x07, 0xAB)
	if v.regAddr != 0x07 {
		t.Errorf("regAddr = $%02X; want $07", v.regAddr)
	}
	if v.regs[0x07] != 0xAB {
		t.Errorf("regs[7] = $%02X; want $AB", v.regs[0x07])
	}
}

// Idle (no key-on) → silence.
func TestVRC7Audio_IdleSilent(t *testing.T) {
	v := NewVRC7Audio()
	for i := 0; i < 1000; i++ {
		if v.Output() != 0 {
			t.Fatalf("idle OPLL emitted %d at sample %d", v.Output(), i)
		}
	}
}

// Key-on a channel with a real instrument → non-zero FM output
// within a short window (attack ramp), then key-off → decays back
// toward silence.
func TestVRC7Audio_KeyOnProducesOutput(t *testing.T) {
	v := NewVRC7Audio()
	// Channel 0: instrument 3 (Piano), volume 0 (loudest).
	writeOPLL(v, 0x30, 0x30)
	// F-number low + (block 4, key-on). F-number ~ 0x180 for an
	// audible mid pitch.
	writeOPLL(v, 0x10, 0x80)
	writeOPLL(v, 0x20, 0x19) // fnum bit8=1 (0x01) + block 4 (0x08) + key-on (0x10)

	sawNonZero := false
	for i := 0; i < 4000 && !sawNonZero; i++ {
		if v.Output() != 0 {
			sawNonZero = true
		}
	}
	if !sawNonZero {
		t.Fatalf("keyed-on OPLL channel never produced output")
	}

	// Key-off → release. Run long enough to decay to idle silence.
	writeOPLL(v, 0x20, 0x09) // same fnum/block (0x08|0x01), key-on cleared
	decayed := false
	for i := 0; i < 200000; i++ {
		if v.Output() == 0 {
			decayed = true
			break
		}
	}
	if !decayed {
		t.Errorf("keyed-off OPLL channel never decayed to silence")
	}
}

// The patch ROM has the 15 fixed instruments populated (index 0 is
// the user patch, left zero).
func TestVRC7Audio_PatchROMPopulated(t *testing.T) {
	for i := 1; i <= 15; i++ {
		empty := true
		for _, b := range opllPatchROM[i] {
			if b != 0 {
				empty = false
				break
			}
		}
		if empty {
			t.Errorf("patch ROM instrument %d is all-zero", i)
		}
	}
}
