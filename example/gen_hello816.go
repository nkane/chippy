//go:build ignore

// gen_hello816 assembles the 65816 playground demo (hello816.s) into a 32 KiB
// $8000-$FFFF image, verifies it on chippy's own 65816 core (it must print the
// banner and STP-halt), and writes web/demos/hello816.bin.
//
//	go run example/gen_hello816.go
package main

import (
	"fmt"
	"os"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/peripheral"
)

const (
	origin  = 0x8000
	banner  = "Hello from the 65816!\n"
	wantOut = banner
)

func main() {
	img := make([]byte, 0x8000) // $8000-$FFFF

	// Program at $8000 (see example/hello816.s). msg sits right after STP.
	msg := origin + 0x16
	prog := []byte{
		0x78,       // sei
		0x18,       // clc
		0xFB,       // xce            ; -> native (E=0)
		0xC2, 0x10, // rep #$10       ; X/Y 16-bit
		0xE2, 0x20, // sep #$20       ; A 8-bit (byte MMIO writes)
		0xA2, 0x00, 0x00, // ldx #$0000
		// loop ($800A):
		0xBD, byte(msg), byte(msg >> 8), // lda msg,x
		0xF0, 0x06, // beq done       ; NUL terminator -> stop
		0x8D, 0x01, 0xF0, // sta $F001       ; text-output MMIO
		0xE8,       // inx
		0x80, 0xF5, // bra loop
		0xDB, // done ($8015): stp
	}
	copy(img, prog)
	copy(img[0x16:], append([]byte(banner), 0x00))

	// Vectors ($FFFA/$FFFC/$FFFE) all point at $8000.
	for _, off := range []int{0x7FFA, 0x7FFC, 0x7FFE} {
		img[off] = byte(origin & 0xFF)
		img[off+1] = byte(origin >> 8)
	}

	if err := verify(img); err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("web/demos/hello816.bin", img, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote web/demos/hello816.bin (%d bytes), verified banner + STP halt\n", len(img))
}

// verify runs the image on a real 65816 core (bank-aware bus + text-output
// peripheral) and checks it prints the banner and halts via STP.
func verify(img []byte) error {
	ram := cpu.NewRAM()
	ram.Load(origin, img)
	mmio := cpu.NewMMIO(ram)
	textOut := peripheral.NewTextOutput(0xF001)
	if err := mmio.Register(textOut); err != nil {
		return err
	}
	c := cpu.NewVariant(mmio, cpu.VariantW65816)
	c.SetBus24(cpu.NewBanked24(mmio))
	c.Reset()

	for steps := 0; steps < 10_000 && !c.Halted; steps++ {
		c.Step()
	}
	if !c.Halted {
		return fmt.Errorf("program never halted (no STP reached)")
	}
	if got := textOut.String(); got != wantOut {
		return fmt.Errorf("banner mismatch: got %q want %q", got, wantOut)
	}
	return nil
}
