; triangle-arpeggio.s — APU triangle channel demo (#250).
;
; Cycles an A-major arpeggio (A4, C#5, E5) on the triangle channel,
; one note every ~0.5 s (driven from the NMI handler). $4008 control
; bit set + non-zero reload so the linear counter never drains; length
; counter loaded large enough that each note sustains until the next
; $400B write rolls in. Screen stays black — audio-only.
;
; Build:  make -C roms/demos triangle-arpeggio
; Run:    nessy roms/demos/triangle-arpeggio/triangle-arpeggio.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG (NROM-128)
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $00, $00
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "VECTORS"
        .word   nmi             ; $FFFA
        .word   reset           ; $FFFC
        .word   irq             ; $FFFE

.segment "ZEROPAGE"
note_idx:       .res 1
frame_cnt:      .res 1

.segment "CODE"

reset:
        sei
        cld
        ldx     #$FF
        txs
        inx                     ; X = 0
        stx     $2000
        stx     $2001
        stx     $4015
        lda     #$40
        sta     $4017

        ; Two-vblank warmup.
:       bit     $2002
        bpl     :-
:       bit     $2002
        bpl     :-

        ; Enable triangle ($4015 bit 2).
        lda     #$04
        sta     $4015

        ; $4008: control bit set (length halt + linear no-decrement)
        ; + max reload value $7F.
        lda     #$FF
        sta     $4008

        lda     #$00
        sta     note_idx
        sta     frame_cnt
        jsr     play_current

        ; Enable NMI.
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
        cmp     #30
        bcc     done
        lda     #$00
        sta     frame_cnt
        inc     note_idx
        lda     note_idx
        cmp     #3
        bcc     :+
        lda     #$00
        sta     note_idx
:       jsr     play_current
done:
        pla
        tax
        pla
        rti

play_current:
        ldx     note_idx
        lda     period_lo,x
        sta     $400A
        lda     period_hi,x
        ora     #$08            ; length idx 1 (doesn't matter, halt set)
        sta     $400B
        rts

irq:
        rti

; Triangle period formula: f = CPU / (32 * (period + 1)).
;   A4  440.00 Hz → period 126
;   C#5 554.37 Hz → period 100
;   E5  659.26 Hz → period  84
period_lo:
        .byte   126, 100,  84
period_hi:
        .byte     0,   0,   0
