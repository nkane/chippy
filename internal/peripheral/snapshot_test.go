package peripheral

import (
	"bytes"
	"testing"
)

// TextOutput Snapshot/Restore must round-trip the buffered bytes and not
// alias the live buffer — mutations after Snapshot must not bleed into
// the captured state.
func TestTextOutputSnapshotRestoreRoundtrip(t *testing.T) {
	tx := NewTextOutput(0xF001)
	for _, b := range []byte("hello") {
		tx.Write(0xF001, b)
	}
	snap := tx.Snapshot()

	// Mutate after snapshot. Captured state must not change.
	tx.Write(0xF001, '!')
	if !bytes.Equal(snap, []byte("hello")) {
		t.Fatalf("snapshot aliased live buf; got %q", snap)
	}

	tx.Restore(snap)
	if got := tx.String(); got != "hello" {
		t.Fatalf("Restore want %q; got %q", "hello", got)
	}
}

// Restoring an empty snapshot must clear the buffer.
func TestTextOutputRestoreEmpty(t *testing.T) {
	tx := NewTextOutput(0xF001)
	tx.Write(0xF001, 'A')
	tx.Restore(nil)
	if tx.Len() != 0 {
		t.Fatalf("Restore(nil) should clear; len=%d", tx.Len())
	}
}

// Keyboard Snapshot/Restore must round-trip both the data byte and the
// ready latch — restoring across an MMIO data read should re-arm the
// peripheral.
func TestKeyboardSnapshotRestoreReadyArmed(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	k.Push('Q')
	snap := k.Snapshot()

	// Drain the latch — emulates the 6502 reading $F004 mid-rewind window.
	_ = k.Read(0xF004)
	if k.Ready() {
		t.Fatalf("precondition: data read should clear ready")
	}

	k.Restore(snap)
	if !k.Ready() {
		t.Fatalf("Restore should re-arm ready flag")
	}
	if v := k.Read(0xF004); v != ('Q' | 0x80) {
		t.Fatalf("Restore should re-arm data; got %02X want %02X", v, byte('Q')|0x80)
	}
}

// Restoring an empty/short snapshot is a hard clear.
func TestKeyboardRestoreShort(t *testing.T) {
	k := NewKeyboardInput(0xF004, 0xF005)
	k.Push('A')
	k.Restore(nil)
	if k.Ready() {
		t.Fatalf("Restore(nil) should clear ready")
	}
	k.Restore([]byte{0x42}) // 1 byte — malformed
	if k.Ready() {
		t.Fatalf("Restore(short) should clear ready")
	}
}
