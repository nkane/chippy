package apu

import "testing"

func TestVRC7Audio_RegCapture(t *testing.T) {
	v := NewVRC7Audio()
	v.Write(0x9010, 0x07)
	v.Write(0x9030, 0xAB)
	if v.regAddr != 0x07 {
		t.Errorf("regAddr = $%02X; want $07", v.regAddr)
	}
	if v.regs[0x07] != 0xAB {
		t.Errorf("regs[7] = $%02X; want $AB", v.regs[0x07])
	}
}

// Stub always outputs silence — until the OPLL synth lands.
func TestVRC7Audio_StubSilent(t *testing.T) {
	v := NewVRC7Audio()
	for i := byte(0); i < 64; i++ {
		v.Write(0x9010, i)
		v.Write(0x9030, 0xFF)
	}
	if v.Output() != 0 {
		t.Errorf("stub emitted %d; want 0 (silent)", v.Output())
	}
}
