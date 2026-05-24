; noise-drum.s — APU noise channel demo (#250).
;
; Alternates between two noise periods every ~0.25 s to mimic a
; kick (low period idx) + snare (high period idx) pattern. Drives
; the LFSR feedback path on the noise channel. Audio-only.
;
; Build:  make -C roms/demos noise-drum
; Run:    nessy roms/demos/noise-drum/noise-drum.nes
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

.segment "ZEROPAGE"
beat_idx:       .res 1
frame_cnt:      .res 1

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

        ; Enable noise ($4015 bit 3).
        lda     #$08
        sta     $4015

        ; $400C: halt + constant volume + vol 8 = $BF.
        ; bit 5 halt → length doesn't drain; bit 4 constant; vol $F = 15.
        ; Use $BF for halt set + constant + vol 15.
        ; Actually want vol ~8 to be ear-safe → $B8.
        lda     #$B8
        sta     $400C
        lda     #$00
        sta     $400D

        lda     #$00
        sta     beat_idx
        sta     frame_cnt
        jsr     play_current

        lda     #$80
        sta     $2000

forever:
        jmp     forever

nmi:
        pha
        txa
        pha
        inc     frame_cnt
        lda     frame_cnt
        cmp     #15             ; ~0.25 s
        bcc     done
        lda     #$00
        sta     frame_cnt
        lda     beat_idx
        eor     #$01            ; toggle 0/1
        sta     beat_idx
        jsr     play_current
done:
        pla
        tax
        pla
        rti

play_current:
        ldx     beat_idx
        lda     period_tbl,x
        sta     $400E
        lda     #$F8            ; length idx (doesn't drain — halt set)
        sta     $400F
        rts

irq:
        rti

; $400E layout: bit 7 = mode (0 = long LFSR), bits 0-3 = period idx.
;   beat 0: low pitch  → period idx $C  (1016 cyc)  — kick
;   beat 1: high pitch → period idx $5  (96 cyc)    — snare
period_tbl:
        .byte   $0C, $05
