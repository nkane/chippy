; count_to_ten.s — increment X from 0 to 10 using a loop, then halt.
;
; Demonstrates: register init, INX, CMP-immediate, conditional branch (BNE).
;
; Build with cc65:
;   ca65 count_to_ten.s -o count_to_ten.o
;   ld65 -C load_five.cfg -o count_to_ten.bin --dbgfile count_to_ten.dbg count_to_ten.o
;
; Run in chippy:
;   chippy -rom example/count_to_ten.bin
;
; Expected final state: X = $0A, Z = 1 (CMP matched).

.segment "CODE"

.proc start
        ldx     #$00            ; X = 0
loop:   inx                     ; X++
        cpx     #$0A            ; reached 10?
        bne     loop            ; no -> keep counting
halt:   jmp     halt            ; spin so the TUI keeps showing final state
.endproc

.segment "VECTORS"
        .word   start           ; NMI   ($FFFA)
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE)
