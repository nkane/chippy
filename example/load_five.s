; load_five.s — load $05 into A, X, Y then halt.
;
; Build with cc65:
;   ca65 load_five.s -o load_five.o
;   ld65 -C load_five.cfg -o load_five.bin --dbgfile load_five.dbg load_five.o
;
; Run in chippy:
;   chippy -rom example/load_five.bin

.segment "CODE"

.proc start
        lda     #$05            ; A = 5
        ldx     #$05            ; X = 5
        ldy     #$05            ; Y = 5
halt:   jmp     halt            ; spin forever so the TUI shows final state
.endproc

; The reset vector lives in its own segment so the linker can place it at $FFFC.
.segment "VECTORS"
        .word   start           ; NMI   ($FFFA) — unused, points at start
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE) — unused, points at start
