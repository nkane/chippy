package cpu

import "testing"

func TestFreeze_SuppressesWrites(t *testing.T) {
	r := NewRAM()
	r.Freeze(0x0200, 0x42)
	if !r.Frozen(0x0200) {
		t.Fatal("Frozen($0200) = false after Freeze")
	}
	r.Write(0x0200, 0xFF) // suppressed
	if got := r.Read(0x0200); got != 0x42 {
		t.Errorf("frozen addr = $%02X after write; want $42 held", got)
	}
	r.Unfreeze(0x0200)
	r.Write(0x0200, 0xFF) // resumes
	if got := r.Read(0x0200); got != 0xFF {
		t.Errorf("after unfreeze = $%02X; want $FF", got)
	}
}

func TestFreeze_ViaCPUStore(t *testing.T) {
	r := NewRAM()
	// $8000: LDA #$FF (A9 FF); STA $0200 (8D 00 02)
	r.Load(0x8000, []byte{0xA9, 0xFF, 0x8D, 0x00, 0x02})
	c := New(r)
	c.PC = 0x8000
	r.Freeze(0x0200, 0x42)
	c.Step() // LDA
	c.Step() // STA $0200 -> suppressed
	if got := r.Read(0x0200); got != 0x42 {
		t.Errorf("$0200 = $%02X after frozen STA; want $42 held", got)
	}
}

func TestFreeze_AddrsAndUnsetCost(t *testing.T) {
	r := NewRAM()
	if len(r.FrozenAddrs()) != 0 {
		t.Fatal("fresh RAM has frozen addrs")
	}
	// Nothing frozen -> writes pass straight through (fast path).
	r.Write(0x10, 0x99)
	if r.Read(0x10) != 0x99 {
		t.Error("write blocked with empty freeze set")
	}
	r.Freeze(0x0300, 0x01)
	r.Freeze(0x0400, 0x02)
	if len(r.FrozenAddrs()) != 2 {
		t.Errorf("FrozenAddrs len = %d; want 2", len(r.FrozenAddrs()))
	}
}
