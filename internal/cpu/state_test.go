package cpu

import (
	"bytes"
	"encoding/gob"
	"testing"
)

// SaveFullState + LoadFullState round-trip every captured field —
// including registers, irq sources, pending stall, and variant.
func TestFullState_RoundTrip(t *testing.T) {
	src := New(NewRAM())
	src.Variant = VariantCMOS65C02
	src.A, src.X, src.Y, src.SP = 0x11, 0x22, 0x33, 0x44
	src.P = FlagN | FlagZ
	src.PC = 0xBEEF
	src.Cycles = 12345
	src.Halted = true
	src.stoppedBySTP = true
	src.extraCycles = 3
	src.pendingStall = 17
	src.AssertIRQSource("foo")
	src.AssertIRQSource("bar")
	src.AssertIRQ() // anonymous (the "" name)
	src.TriggerNMI()

	s := src.SaveFullState()

	// Round through gob to confirm the wire format is stable —
	// CPU loads from disk go through gob in cmd/nessy.
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		t.Fatalf("gob encode: %v", err)
	}
	var s2 FullState
	if err := gob.NewDecoder(&buf).Decode(&s2); err != nil {
		t.Fatalf("gob decode: %v", err)
	}

	dst := New(NewRAM())
	dst.Variant = VariantNMOS // wrong on purpose — LoadFullState should fix
	dst.LoadFullState(s2)

	if dst.A != src.A || dst.X != src.X || dst.Y != src.Y || dst.SP != src.SP || dst.P != src.P {
		t.Errorf("registers mismatch")
	}
	if dst.PC != src.PC || dst.Cycles != src.Cycles {
		t.Errorf("PC/Cycles mismatch: %X / %d vs %X / %d", dst.PC, dst.Cycles, src.PC, src.Cycles)
	}
	if dst.Variant != VariantCMOS65C02 {
		t.Errorf("variant not restored: %s", dst.Variant)
	}
	if !dst.Halted || !dst.stoppedBySTP {
		t.Errorf("halt flags not restored")
	}
	if dst.extraCycles != 3 || dst.pendingStall != 17 {
		t.Errorf("cycle bookkeeping mismatch")
	}
	if !dst.irqLine || !dst.nmiPending {
		t.Errorf("interrupt lines not restored")
	}
	for _, want := range []string{"foo", "bar", ""} {
		if _, ok := dst.irqSources[want]; !ok {
			t.Errorf("irqSources missing %q", want)
		}
	}
	if dst.opcodes == nil {
		t.Errorf("opcode table not re-bound post-restore")
	}
}

// LoadFullState rejects a RAM payload of the wrong size.
func TestRAM_LoadFullState_BadSize(t *testing.T) {
	r := NewRAM()
	if err := r.LoadFullState([]byte{1, 2, 3}); err == nil {
		t.Errorf("expected error on short payload")
	}
}

// RAM round-trip preserves every byte + resets the shadow epoch.
func TestRAM_FullState_RoundTrip(t *testing.T) {
	src := NewRAM()
	src.EnableShadow()
	for i := range 0x10000 {
		src.Data[i] = byte(i ^ 0x5A)
	}
	s := src.SaveFullState()

	dst := NewRAM()
	dst.EnableShadow()
	dst.Write(0x40, 0x99) // mutates shadow; LoadFullState must clear it
	if err := dst.LoadFullState(s); err != nil {
		t.Fatalf("load: %v", err)
	}
	for i := range 0x10000 {
		if dst.Data[i] != byte(i^0x5A) {
			t.Fatalf("Data[%d] = %d; want %d", i, dst.Data[i], byte(i^0x5A))
		}
	}
	if len(dst.shadow) != 0 {
		t.Errorf("shadow epoch not reset post-load: %d pages", len(dst.shadow))
	}
}
