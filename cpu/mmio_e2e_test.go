package cpu_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/loader"
	"github.com/nkane/chippy/peripheral"
)

// TestMMIOHelloDemo_E2E loads example/hello.bin under a MMIO bus that
// routes $F001 writes to a TextOutput peripheral, and verifies the
// peripheral's buffer ends with "HELLO\n" — covering the acceptance
// criterion for issue #16.
func TestMMIOHelloDemo_E2E(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Skip("can't locate test file")
	}
	bin := filepath.Join(filepath.Dir(thisFile), "..", "..", "example", "hello.bin")

	ram := cpu.NewRAM()
	if _, err := loader.Load(ram, bin, loader.Options{Addr: 0x8000}); err != nil {
		t.Skipf("hello.bin not built (run `make -C example hello.bin`): %v", err)
	}

	mmio := cpu.NewMMIO(ram)
	out := peripheral.NewTextOutput(0xF001)
	if err := mmio.Register(out); err != nil {
		t.Fatalf("Register: %v", err)
	}

	c := cpu.New(mmio)
	for i := 0; i < 200 && !c.Halted; i++ {
		c.Step()
	}
	if !c.Halted {
		t.Fatalf("program never halted; PC=%04X", c.PC)
	}

	// hello.s emits "HELLO" + CR; the peripheral translates CR to LF.
	if got := out.String(); got != "HELLO\n" {
		t.Fatalf("output = %q, want %q", got, "HELLO\n")
	}

	// Bytes that went to MMIO must NOT have leaked into RAM.
	if ram.Data[0xF001] != 0 {
		t.Fatalf("RAM at $F001 should be untouched; got %02X", ram.Data[0xF001])
	}
}

// TestMMIOKeyboard_E2E uses a tiny inline program to verify the keyboard
// status/data path through MMIO without depending on an assembled .bin.
//
// Program (at $8000):
//
//	loop:  LDA $F005       ; AD 05 F0  ; poll status (bit7 = key ready)
//	       BPL loop        ; 10 FB     ; loop while bit7 clear
//	       LDA $F004       ; AD 04 F0  ; read data — clears ready
//	       STA $0010       ; 85 10     ; store at $0010 so the test can assert
//	stop:  JMP stop        ; 4C 0A 80
//
// Push a key, run the program, expect $0010 = key | 0x80.
func TestMMIOKeyboard_E2E(t *testing.T) {
	ram := cpu.NewRAM()
	prog := []byte{
		0xAD, 0x05, 0xF0, // LDA $F005
		0x10, 0xFB, //       BPL loop  (-5)
		0xAD, 0x04, 0xF0, // LDA $F004
		0x85, 0x10, //       STA $10
		0x4C, 0x0A, 0x80, // JMP $800A  (stop:)
	}
	ram.Load(0x8000, prog)
	ram.Write(cpu.VecReset, 0x00)
	ram.Write(cpu.VecReset+1, 0x80)

	mmio := cpu.NewMMIO(ram)
	kb := peripheral.NewKeyboardInput(0xF004, 0xF005)
	if err := mmio.Register(kb); err != nil {
		t.Fatalf("Register: %v", err)
	}

	c := cpu.New(mmio)
	// Push 'Q' before stepping — program polls and reads on first ready.
	kb.Push('Q')

	for i := 0; i < 200 && !c.Halted; i++ {
		c.Step()
	}
	if !c.Halted {
		t.Fatalf("program never halted; PC=%04X", c.PC)
	}
	if got := ram.Read(0x0010); got != ('Q' | 0x80) {
		t.Fatalf("$0010 = %02X, want %02X", got, byte('Q')|0x80)
	}
	if kb.Ready() {
		t.Fatalf("keyboard Ready should have drained on data read")
	}
}
