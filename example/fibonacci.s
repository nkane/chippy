; fibonacci.s — compute Fibonacci numbers in zero page until overflow, then halt.
;
; Demonstrates: zero-page storage, ADC with carry tracking, BCC branch on
; carry-out, indexed store into a result table.
;
; Layout:
;   $00     prev   (F[n-1])
;   $01     curr   (F[n])
;   $02     idx    (next slot in $0200..)
;   $0200+  result table of 8-bit Fibonacci values, terminated when overflow hits.
;
; Sequence stored at $0200: 01 01 02 03 05 08 0D 15 22 37 59 90 E9 (next would
; overflow 8 bits so we stop). Total 13 entries.
;
; Build with cc65:
;   ca65 fibonacci.s -o fibonacci.o
;   ld65 -C load_five.cfg -o fibonacci.bin --dbgfile fibonacci.dbg fibonacci.o

.segment "CODE"

prev = $00
curr = $01
idx  = $02

.proc start
        lda     #$01
        sta     prev            ; F[0] = 1
        sta     curr            ; F[1] = 1
        ldx     #$00
        stx     idx

        ; Store the two seed values at $0200, $0201.
        sta     $0200
        sta     $0201
        lda     #$02
        sta     idx

next:   clc
        lda     prev
        adc     curr            ; A = prev + curr
        bcs     done            ; overflow -> stop
        ldx     idx
        sta     $0200,x         ; result[idx] = A
        inx
        stx     idx
        ; Shift window: prev <- curr, curr <- A.
        ldy     curr
        sty     prev
        sta     curr
        jmp     next

done:   ; idx holds the count of stored entries.
halt:   jmp     halt
.endproc

.segment "VECTORS"
        .word   start           ; NMI   ($FFFA)
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE)
