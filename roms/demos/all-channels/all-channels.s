; all-channels.s — kitchen-sink APU demo exercising the non-linear
; DAC mixer (#249, #250). All four wave channels enabled simultaneously
; at fixed pitches, no NMI dynamics — just a sustained chord. Confirms
; that the mixer combines pulse + triangle + noise correctly under
; load.
;
; Build:  make -C roms/demos all-channels
; Run:    nessy roms/demos/all-channels/all-channels.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01
        .byte   $01
        .byte   $00, $00
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "VECTORS"
        .word   nmi
        .word   reset
        .word   irq

.segment "CODE"

reset:
        sei
        cld
        ldx     #$FF
        txs
        inx
        stx     $2000
        stx     $2001
        stx     $4015
        lda     #$40
        sta     $4017

:       bit     $2002
        bpl     :-
:       bit     $2002
        bpl     :-

        ; Enable pulse1 + pulse2 + triangle + noise ($4015 bits 0+1+2+3 = $0F).
        lda     #$0F
        sta     $4015

        ; ---- Pulse 1: 50% duty, halt, constant vol 6, period for A4 (253).
        lda     #$B6
        sta     $4000
        lda     #$00
        sta     $4001
        lda     #253
        sta     $4002
        lda     #$08
        sta     $4003

        ; ---- Pulse 2: 25% duty, halt, constant vol 6, period for E5 (169).
        lda     #$76            ; duty 01 (25%), halt 1, constant 1, vol 6
        sta     $4004
        lda     #$00
        sta     $4005
        lda     #169
        sta     $4006
        lda     #$08
        sta     $4007

        ; ---- Triangle: control bit set + max reload; period for C#5 (100).
        lda     #$FF
        sta     $4008
        lda     #100
        sta     $400A
        lda     #$08
        sta     $400B

        ; ---- Noise: halt, constant, vol 4 (quieter so it doesn't dominate),
        ; period idx $A (380 cycles).
        lda     #$B4
        sta     $400C
        lda     #$0A
        sta     $400E
        lda     #$F8
        sta     $400F

        lda     #$00
        sta     $2000           ; NMI off; demo is static

forever:
        jmp     forever

nmi:
        rti

irq:
        rti
