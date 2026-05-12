; mul16.s — 16x16 → 32-bit unsigned multiply.
;
; Demonstrates: zero-page state, shift-and-add multiply, ADC carry
; propagation across word boundaries, RMW INC on a multi-byte counter.
;
; Inputs (set before reset):
;     OP1L/OP1H = $1234
;     OP2L/OP2H = $5678
; Expected result at PRODUCT0..PRODUCT3 (little-endian): $06260060
;     $1234 * $5678 = $06260060 (decimal: 4660 * 22136 = 102 793 056)
;
; Watching tips:
;   :watch $0040 word "OP1"
;   :watch $0042 word "OP2"
;   :watch $0044 byte "BITS"
;   :watch $0050 byte "P0" / $0051 word "P1+2"
;   Step through the bit loop with `s`; observe SHIFTL rotating OP1
;   while PRODUCT accumulates OP2 partial sums on every set bit.

.segment "ZEROPAGE"
OP1L     = $40
OP1H     = $41
OP2L     = $42
OP2H     = $43
BITS     = $44
PRODUCT0 = $50         ; LSB
PRODUCT1 = $51
PRODUCT2 = $52
PRODUCT3 = $53         ; MSB

.segment "CODE"

.proc start
        ; Load operands.
        lda     #$34
        sta     OP1L
        lda     #$12
        sta     OP1H
        lda     #$78
        sta     OP2L
        lda     #$56
        sta     OP2H

        ; Zero product.
        lda     #0
        sta     PRODUCT0
        sta     PRODUCT1
        sta     PRODUCT2
        sta     PRODUCT3

        ; 16 bits to process.
        lda     #16
        sta     BITS

bit_loop:
        ; Shift OP1 right; LSB into carry.
        lsr     OP1H
        ror     OP1L

        bcc     no_add
        ; Carry set → add OP2 into PRODUCT[2..0]  (PRODUCT3 is MSB of carry).
        clc
        lda     PRODUCT2
        adc     OP2L
        sta     PRODUCT2
        lda     PRODUCT3
        adc     OP2H
        sta     PRODUCT3

no_add:
        ; Shift the 32-bit product right (carry from last add cascades down).
        lsr     PRODUCT3
        ror     PRODUCT2
        ror     PRODUCT1
        ror     PRODUCT0

        dec     BITS
        bne     bit_loop

halt:   jmp     halt

.endproc

.segment "VECTORS"
        .word   start
        .word   start
        .word   start
