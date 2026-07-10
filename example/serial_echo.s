; serial_echo.s — 6551 ACIA serial echo, Ben-Eater-kit style. Reads the
; ACIA at $5000 and echoes each received byte straight back. Run it with
; the ACIA wired at that base:
;
;   chippy -rom example/serial_echo.bin -acia '$5000'
;
; then press `i` (input mode) and type — each key echoes into the Serial
; pane. Build the .bin first (like the ca65 demos' `make`):
;   go run example/gen_serial_echo.go
;
; Demonstrates: the 6551 register file, polled RX (RDRF) / TX (TDRE),
; and chippy's -acia wiring. Receiver interrupts are disabled here (COMMAND
; bit 1 set) so the loop is pure polling; enable them (bit 1 clear) plus an
; IRQ handler for an interrupt-driven console.

ACIA_DATA    = $5000            ; read = RX byte, write = TX byte
ACIA_STATUS  = $5001            ; bit 3 RDRF (rx full), bit 4 TDRE (tx empty)
ACIA_COMMAND = $5002
ACIA_CONTROL = $5003

RDRF         = $08
TDRE         = $10

.segment "CODE"

.proc start
        cld
        ldx     #$FF
        txs

        lda     #$1E            ; 8N1, 19200 baud — cosmetic in chippy
        sta     ACIA_CONTROL
        lda     #$0B            ; DTR on, RTS on, receiver IRQ disabled
        sta     ACIA_COMMAND

rxloop:
        lda     ACIA_STATUS
        and     #RDRF
        beq     rxloop          ; spin until a byte arrives
        lda     ACIA_DATA       ; consume it (clears RDRF)
        pha

txwait:
        lda     ACIA_STATUS
        and     #TDRE
        beq     txwait          ; wait for the transmitter (always ready here)
        pla
        sta     ACIA_DATA       ; echo it back
        jmp     rxloop
.endproc

.segment "VECTORS"
        .word   start
        .word   start
        .word   start
