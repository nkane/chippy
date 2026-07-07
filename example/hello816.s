; hello816.s — WDC 65C816 native-mode "hello world" for the playground.
;
; Demonstrates: emulation -> native switch (CLC/XCE), REP/SEP width control
; (16-bit index, 8-bit accumulator), 16-bit indexed load (LDA abs,X), MMIO
; text output at $F001, and STP to halt.
;
; This is the source of record for web/demos/hello816.bin. The .bin is
; hand-assembled + verified by example/gen_hello816.go (`go run`), so it does
; not require cc65 on PATH; build with cc65 if you prefer:
;   ca65 --cpu 65816 hello816.s -o hello816.o
;   ld65 -C <cfg> -o hello816.bin hello816.o
;
; Run in chippy:  chippy -cpu 65816 -rom example/hello816.bin
; Expected: prints "Hello from the 65816!" then STP-halts.

.p816

.segment "CODE"

.proc start
        sei                     ; no interrupts
        clc
        xce                     ; C->E swap: emulation off, native mode (E=0)
        rep     #$10            ; X/Y 16-bit
        sep     #$20            ; A 8-bit (one byte per MMIO write)
        ldx     #$0000
loop:   lda     msg,x           ; 8-bit load, 16-bit index
        beq     done            ; NUL terminator -> stop
        sta     $F001           ; text-output MMIO
        inx
        bra     loop
done:   stp                     ; halt the core

msg:    .byte   "Hello from the 65816!", $0A, $00
.endproc

.segment "VECTORS"
        .word   start           ; NMI   ($FFFA)
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE)
