package peripheral

import "testing"

func TestKeyboardStatusEmpty(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	if v := k.Read(0xF005); v != 0x00 {
		t.Fatalf("status should be 0 before any key; got %02X", v)
	}
	if k.Ready() {
		t.Fatalf("Ready should be false initially")
	}
}

func TestKeyboardPushSetsReady(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	k.Push('A')
	if !k.Ready() {
		t.Fatalf("Ready should be true after Push")
	}
	if v := k.Read(0xF005); v != 0x80 {
		t.Fatalf("status should be 0x80 with key pending; got %02X", v)
	}
	// Reading status must NOT clear ready — only data read clears it.
	if !k.Ready() {
		t.Fatalf("status read must not drain ready")
	}
}

func TestKeyboardDataReadClearsReady(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	k.Push('X')
	if v := k.Read(0xF004); v != ('X' | 0x80) {
		t.Fatalf("data read want %02X; got %02X", 'X'|0x80, v)
	}
	if k.Ready() {
		t.Fatalf("data read must clear ready")
	}
	if v := k.Read(0xF005); v != 0x00 {
		t.Fatalf("status should be 0 after data drain; got %02X", v)
	}
}

func TestKeyboardLatchOverwritesPrev(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	k.Push('A')
	k.Push('B') // overwrite — Apple-1 single-byte latch
	if v := k.Read(0xF004); v != ('B' | 0x80) {
		t.Fatalf("want B; got %02X", v)
	}
}

func TestKeyboardWriteIgnored(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	k.Push('A')
	k.Write(0xF004, 0xFF)
	k.Write(0xF005, 0xFF)
	if !k.Ready() {
		t.Fatalf("writes should not drain ready")
	}
	if v := k.Read(0xF004); v != ('A' | 0x80) {
		t.Fatalf("write should not change data; got %02X", v)
	}
}

func TestKeyboardRange(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	lo, hi := k.Range()
	if lo != 0xF004 || hi != 0xF005 {
		t.Fatalf("Range want F004,F005; got %04X,%04X", lo, hi)
	}
	// Reversed construction still produces canonical range.
	k2 := NewKeyboardInput(0xF005, 0xF004)
	lo, hi = k2.Range()
	if lo != 0xF004 || hi != 0xF005 {
		t.Fatalf("reversed Range want F004,F005; got %04X,%04X", lo, hi)
	}
}
