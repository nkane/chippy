; vblank-bounce.s — nessy's third homemade demo (issue #196 / epic #193).
;
; A single 8×8 tile bounces inside the playfield. Each NMI handler:
;   1. Erases the tile at the previous position.
;   2. Advances (x, y) by (dir_x, dir_y), flipping direction at
;      bounds [1, 30] for x and [1, 28] for y.
;   3. Draws the tile at the new position.
;   4. Restores PPUSCROLL (mandatory after $2006 writes).
;
; Exercises:
;   - PPU NMI line (PPUCTRL bit 7 enables; vblank @ scanline 241
;     raises it).
;   - CPU NMI service routine (push P, push PC, vector $FFFA, RTI).
;   - Per-NMI nametable updates inside the vblank window.
;
; Main loop just spins on `JMP self` — all per-frame work happens
; in the NMI handler. Canonical NES idle pattern.

.zeropage
pos_x:  .res 1
pos_y:  .res 1
dir_x:  .res 1          ; +1 or $FF (-1) via two's complement
dir_y:  .res 1

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01, $01, $00, $00
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
        stx     $2000           ; PPUCTRL = 0 (NMI off during setup)
        stx     $2001           ; PPUMASK = 0
        stx     $4015
        lda     #$40
        sta     $4017

        ; 1st vblank wait
:       bit     $2002
        bpl     :-

        ; clear CPU RAM
        lda     #$00
        ldx     #$00
clear_ram:
        sta     $0000,x
        sta     $0100,x
        sta     $0200,x
        sta     $0300,x
        sta     $0400,x
        sta     $0500,x
        sta     $0600,x
        sta     $0700,x
        inx
        bne     clear_ram

        ; 2nd vblank wait
:       bit     $2002
        bpl     :-

        ; palette: bg dark blue + white sub-palette
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     #$01
        sta     $2007
        lda     #$30
        sta     $2007

        ; clear nametable
        lda     #$20
        sta     $2006
        lda     #$00
        sta     $2006
        ldy     #4
        ldx     #0
        lda     #$00
clear_nt:
        sta     $2007
        inx
        bne     clear_nt
        dey
        bne     clear_nt

        ; init bounce state — start mid-playfield, moving down-right
        lda     #16
        sta     pos_x
        lda     #14
        sta     pos_y
        lda     #$01
        sta     dir_x
        sta     dir_y

        ; draw initial tile so frame 1 sees something even before
        ; NMI fires
        jsr     draw_tile_at_pos

        ; reset scroll
        lda     #$00
        sta     $2005
        sta     $2005

        ; enable BG show + NMI
        lda     #$0A
        sta     $2001
        lda     #$80            ; PPUCTRL bit 7 = NMI on
        sta     $2000

forever:
        jmp     forever

nmi:
        pha
        txa
        pha
        tya
        pha

        ; --- erase tile at CURRENT pos (where the previous NMI / the
        ;     reset drew). Using pos_x / pos_y directly here means
        ;     we don't need an old_x / old_y pair — the move below
        ;     swings pos to the new cell after the erase commits.
        jsr     erase_tile_at_pos

        ; --- advance x with bounce
        lda     pos_x
        clc
        adc     dir_x
        sta     pos_x
        cmp     #30             ; >= 30? bounce right
        bcc     not_right_edge
        lda     #29
        sta     pos_x
        lda     #$FF
        sta     dir_x
        jmp     after_x_check
not_right_edge:
        lda     pos_x
        cmp     #2              ; < 2? bounce left
        bcs     after_x_check
        lda     #2
        sta     pos_x
        lda     #$01
        sta     dir_x
after_x_check:

        ; --- advance y with bounce
        lda     pos_y
        clc
        adc     dir_y
        sta     pos_y
        cmp     #28
        bcc     not_bottom_edge
        lda     #27
        sta     pos_y
        lda     #$FF
        sta     dir_y
        jmp     after_y_check
not_bottom_edge:
        lda     pos_y
        cmp     #2
        bcs     after_y_check
        lda     #2
        sta     pos_y
        lda     #$01
        sta     dir_y
after_y_check:

        ; --- draw new tile at the post-advance pos
        jsr     draw_tile_at_pos

        ; --- restore scroll (every PPUADDR write clobbers the latch)
        lda     #$00
        sta     $2005
        sta     $2005

        pla
        tay
        pla
        tax
        pla
        rti

irq:
        rti

; set_ppuaddr_pos: programs PPUADDR for the cell at (pos_x, pos_y).
; Clobbers A.
set_ppuaddr_pos:
        lda     pos_y
        lsr
        lsr
        lsr
        clc
        adc     #$20
        sta     $2006           ; PPUADDR hi = $20 + (pos_y >> 3)
        lda     pos_y
        and     #$07
        asl
        asl
        asl
        asl
        asl
        ora     pos_x
        sta     $2006           ; PPUADDR lo = ((pos_y & 7) << 5) | pos_x
        rts

; draw_tile_at_pos: writes the full-block tile ($31) at (pos_x, pos_y).
draw_tile_at_pos:
        jsr     set_ppuaddr_pos
        lda     #$31
        sta     $2007
        rts

; erase_tile_at_pos: writes blank tile ($00) at (pos_x, pos_y).
erase_tile_at_pos:
        jsr     set_ppuaddr_pos
        lda     #$00
        sta     $2007
        rts

; -------------------------------- CHR-ROM (8 KiB).
.segment "CHARS"
        ; Tiles $00-$30: blank
        .res    16 * $31, $00

        ; Tile $31: solid block (the bouncing tile)
        .byte   $FF, $FF, $FF, $FF, $FF, $FF, $FF, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $32-$FF: blank
        .res    16 * ($100 - $32), $00

        ; Pattern table 1 unused
        .res    16 * 256, $00
