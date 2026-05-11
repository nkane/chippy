; hello.s — write "HELLO\n" to the TextOutput peripheral, then halt.
;
; Demonstrates: memory-mapped I/O. The TextOutput peripheral lives at
;   $F001 (write-only). Each byte written is appended to the TUI's
;   Output panel. The KeyboardInput peripheral (not used here) lives at
;   $F004/$F005.
;
; Build with cc65:
;   ca65 hello.s -o hello.o
;   ld65 -C load_five.cfg -o hello.bin --dbgfile hello.dbg hello.o
;
; Run in chippy:
;   chippy -rom example/hello.bin

OUT     = $F001

.segment "CODE"

.proc start
        ldx     #$00            ; index into the message
loop:   lda     msg,x           ; load next byte
        beq     done            ; zero terminator -> stop
        sta     OUT             ; emit to TextOutput
        inx
        bne     loop            ; <256 chars guaranteed
done:   jmp     done            ; spin so the final state stays on screen
.endproc

msg:    .byte   "HELLO", $0D, $00

.segment "VECTORS"
        .word   start           ; NMI   ($FFFA)
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE)
