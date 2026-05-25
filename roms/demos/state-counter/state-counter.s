; state-counter.s — save-state round-trip probe (#327).
;
; Increments a zero-page counter every NMI + writes that byte to
; the universal BG color register ($3F00) so the framebuffer's
; colour is a 1:1 function of the frame index. Save / load tests
; can then assert "post-load BG colour matches the saved frame's"
; without any per-pixel SHA fiddling.
;
; Build:  make -C roms/demos state-counter
; Run:    nessy roms/demos/state-counter/state-counter.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG (NROM-128)
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $00, $00
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "VECTORS"
        .word   nmi
        .word   reset
        .word   irq

.segment "ZEROPAGE"
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

        ; Two-vblank warmup.
:       bit     $2002
        bpl     :-
:       bit     $2002
        bpl     :-

        ; Enable BG show (so the universal palette colour fills
        ; the screen) + NMI on vblank.
        lda     #$08
        sta     $2001
        lda     #$80
        sta     $2000
        cli
:       jmp     :-

nmi:
        inc     frame_cnt

        ; Write frame_cnt to $3F00 (universal BG colour). Result:
        ; framebuffer fills with paletteRGB(frame_cnt & 0x3F) every
        ; frame — colour is a 1:1 function of NMI count.
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     frame_cnt
        sta     $2007

        ; Reset scroll latch.
        lda     #$00
        sta     $2005
        sta     $2005
        rti

irq:
        rti
