//go:build ignore

// gen_serial_echo hand-assembles the 6551-ACIA serial-echo demo, verifies it
// on chippy's own 6502 core (feed a few RX bytes, confirm they echo back out
// the TX sink), and writes example/serial_echo.bin.
//
//	go run example/gen_serial_echo.go
//
// Run it in the TUI with the ACIA wired at the Ben Eater address:
//
//	chippy -rom example/serial_echo.bin -acia '$5000'
//
// then press `i` and type — each key echoes into the Serial pane.
package main

import (
	"fmt"
	"os"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/peripheral"
)

const (
	origin   = 0x8000
	aciaBase = 0x5000 // Ben Eater 6502 kit convention

	aciaData    = aciaBase + 0 // read = RX byte, write = TX byte
	aciaStatus  = aciaBase + 1 // bit 3 RDRF, bit 4 TDRE
	aciaCommand = aciaBase + 2
	aciaControl = aciaBase + 3
)

func main() {
	img := make([]byte, 0x8000) // $8000-$FFFF

	// Serial echo: init the ACIA (no interrupts — polled), then loop reading
	// RX when RDRF is set and writing it straight back when TDRE is set. See
	// example/serial_echo.s for the annotated source.
	lo := func(a int) byte { return byte(a & 0xFF) }
	hi := func(a int) byte { return byte(a >> 8) }
	prog := []byte{
		0xD8,       // cld
		0xA2, 0xFF, // ldx #$FF
		0x9A,       // txs
		0xA9, 0x1E, // lda #$1E
		0x8D, lo(aciaControl), hi(aciaControl), // sta CONTROL  ; 8N1, cosmetic here
		0xA9, 0x0B, // lda #$0B     ; DTR on, RX IRQ disabled
		0x8D, lo(aciaCommand), hi(aciaCommand), // sta COMMAND
		// rxloop ($800E):
		0xAD, lo(aciaStatus), hi(aciaStatus), // lda STATUS
		0x29, 0x08, // and #$08     ; RDRF
		0xF0, 0xF9, // beq rxloop
		0xAD, lo(aciaData), hi(aciaData), // lda DATA     ; consume RX byte
		0x48, // pha
		// txwait ($8019):
		0xAD, lo(aciaStatus), hi(aciaStatus), // lda STATUS
		0x29, 0x10, // and #$10     ; TDRE
		0xF0, 0xF9, // beq txwait
		0x68,                             // pla
		0x8D, lo(aciaData), hi(aciaData), // sta DATA     ; echo (TX)
		0x4C, lo(origin + 0x0E), hi(origin), // jmp rxloop
	}
	copy(img, prog)

	// Vectors ($FFFA/$FFFC/$FFFE) all point at $8000.
	for _, off := range []int{0x7FFA, 0x7FFC, 0x7FFE} {
		img[off] = byte(origin & 0xFF)
		img[off+1] = byte(origin >> 8)
	}

	if err := verify(img); err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("example/serial_echo.bin", img, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote example/serial_echo.bin (%d bytes), verified ACIA echo\n", len(img))
}

// verify runs the image on a 6502 core with a 6551 ACIA at $5000, queues a
// short input string, and checks the program echoes it back out the TX sink.
func verify(img []byte) error {
	const want = "Hello, 6551!"

	ram := cpu.NewRAM()
	ram.Load(origin, img)
	mmio := cpu.NewMMIO(ram)
	acia := peripheral.NewACIA(aciaBase)
	if err := mmio.Register(acia); err != nil {
		return err
	}
	c := cpu.NewVariant(mmio, cpu.VariantNMOS)
	c.Reset()

	for _, b := range []byte(want) {
		acia.Receive(b)
	}
	for steps := 0; steps < 100_000 && acia.RxPending() > 0; steps++ {
		c.Step()
	}
	// A few extra steps so the last byte finishes its TX write.
	for steps := 0; steps < 100; steps++ {
		c.Step()
	}
	if got := acia.TxString(); got != want {
		return fmt.Errorf("echo mismatch: got %q want %q", got, want)
	}
	return nil
}
