; stack_demo.s — push three bytes, pull them back into different registers.
;
; Demonstrates: stack pointer behavior, PHA, PLA, and TAX/TAY for moving
; the pulled value into X / Y.
;
; Push order: $11, $22, $33  (SP decrements each push)
; Pull order: pulls in reverse so:
;   A <- $33 (then TAX -> X = $33)
;   A <- $22 (then TAY -> Y = $22)
;   A <- $11 (left in A)
;
; Final state: A=$11, X=$33, Y=$22, SP back to $FF.
;
; Build with cc65:
;   ca65 stack_demo.s -o stack_demo.o
;   ld65 -C load_five.cfg -o stack_demo.bin --dbgfile stack_demo.dbg stack_demo.o

.segment "CODE"

.proc start
        ldx     #$FF            ; reset stack pointer to top of page 1
        txs

        lda     #$11
        pha                     ; push $11
        lda     #$22
        pha                     ; push $22
        lda     #$33
        pha                     ; push $33

        pla                     ; A = $33
        tax                     ; X = $33
        pla                     ; A = $22
        tay                     ; Y = $22
        pla                     ; A = $11
halt:   jmp     halt
.endproc

.segment "VECTORS"
        .word   start           ; NMI   ($FFFA)
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE)
