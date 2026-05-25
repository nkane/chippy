; oam-grid.s — OAMDMA 64-sprite stress demo (#326).
;
; Fills OAM with an 8x8 grid of sprites (all tile $30 = a solid
; square in the embedded CHR) centred near the playfield middle.
; NMI handler refreshes OAM each frame via $4014 OAMDMA from the
; CPU page at $0200-$02FF — the canonical NES sprite pipeline.
;
; Validates the v0.2 OAMDMA path (#204) + 64-sprite OAM walk +
; sprite priority logic under a SHA-pinned framebuffer regression.
;
; Build:  make -C roms/demos oam-grid
; Run:    nessy roms/demos/oam-grid/oam-grid.nes
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

        ; Palette: $3F00 = $0F (black BG); $3F11 (sprite palette 0
        ; entry 1) = $30 (white).
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     #$0F
        sta     $2007           ; $3F00 = black

        lda     #$3F
        sta     $2006
        lda     #$11
        sta     $2006
        lda     #$30
        sta     $2007           ; $3F11 = white

        ; Build OAM buffer at $0200: 64 sprites in an 8x8 grid,
        ; top-left at (88, 88). Each sprite = 4 bytes (y, tile, attr, x).
        ldx     #$00            ; OAM byte cursor
        ldy     #$00            ; sprite index 0..63
build_loop:
        ; Y position: 88 + (sprite_idx >> 3) * 8 — same row for
        ; sprites 0..7, etc.
        tya
        lsr a
        lsr a
        lsr a                   ; A = row 0..7
        asl a
        asl a
        asl a                   ; A = row * 8
        clc
        adc #88
        sta $0200,x             ; OAM[x+0] = Y
        inx

        ; Tile = $30 (a solid square in the embedded CHR — see
        ; below).
        lda #$30
        sta $0200,x
        inx

        ; Attributes: palette 0, no flip, no priority.
        lda #$00
        sta $0200,x
        inx

        ; X position: 88 + (sprite_idx & 7) * 8.
        tya
        and #$07
        asl a
        asl a
        asl a
        clc
        adc #88
        sta $0200,x
        inx

        iny
        cpy #64
        bne build_loop

        ; First DMA to seed OAM before BG show enables sprite render.
        lda #$02
        sta $4014

        ; Enable sprite show ($2001 bit 4). Skip BG show — pure
        ; sprite-layer demo.
        lda #$10
        sta $2001

        ; Enable NMI on vblank.
        lda #$80
        sta $2000
        cli
:       jmp :-

nmi:
        ; Refresh OAM each frame from the CPU-page buffer.
        lda #$02
        sta $4014
        rti

irq:
        rti

; ===========================================================================
; CHR-ROM — pattern table. We need tile $30 (offset $300 in CHR)
; to be a solid 8x8 square in palette colour 1. Plane 0 = $FF rows,
; plane 1 = $00 rows. The rest of the CHR is zero-filled by the
; cfg's fillval.
; ===========================================================================
.segment "CHARS"
        .res $300              ; pad through tile 0..$2F
        .repeat 8              ; tile $30 plane 0: 8 × $FF
        .byte $FF
        .endrepeat
        .repeat 8              ; tile $30 plane 1: 8 × $00
        .byte $00
        .endrepeat
        ; Rest of CHR pad-fills via cfg.
