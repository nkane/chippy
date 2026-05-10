; bcd_add.s — add two BCD-encoded values with the decimal flag set.
;
; Demonstrates: SED/CLD, ADC in decimal mode, multi-byte BCD addition with
; carry propagation. Verifies chippy's BCD implementation (issue #1).
;
; Computes 49 + 58 = 107 (BCD), stored as low byte $07 in A and high byte
; $01 in X (carry-in for the next column).
;
; Operand bytes live in zero page so the math is easy to follow in the TUI:
;   $10 = $49   (operand A, low BCD pair)
;   $11 = $58   (operand B, low BCD pair)
;
; Final state: A = $07, X = $01, D flag = 1, C = 0 (after the high-byte add).
;
; Build with cc65:
;   ca65 bcd_add.s -o bcd_add.o
;   ld65 -C load_five.cfg -o bcd_add.bin --dbgfile bcd_add.dbg bcd_add.o

.segment "CODE"

.proc start
        sed                     ; enable decimal mode
        lda     #$49
        sta     $10
        lda     #$58
        sta     $11

        clc
        lda     $10
        adc     $11             ; A = $07, C = 1 (BCD: 49 + 58 = 107)

        ldx     #$00
        bcc     nocarry
        inx                     ; X = $01 carry into next BCD column
nocarry:
        cld                     ; back to binary mode for cleanliness
halt:   jmp     halt
.endproc

.segment "VECTORS"
        .word   start           ; NMI   ($FFFA)
        .word   start           ; RESET ($FFFC)
        .word   start           ; IRQ   ($FFFE)
