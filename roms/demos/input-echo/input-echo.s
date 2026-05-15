; input-echo.s — nessy's second homemade demo (issue #195 / epic #193).
;
; Eight indicator boxes arranged in a controller layout. Each frame
; the program strobes $4016 and reads 8 button bits; each indicator
; tile flips between empty ($30) and full ($31) to reflect press
; state. Exercises:
;   - $4016 strobe + serial shift (joypad peripheral).
;   - Per-frame PPU writes inside vblank window.
;   - Mixed BG + nametable-update loop.
;
; Layout (centered on screen):
;
;       [U]
;
;   [L]     [R]      [SE][ST]      [A][B]
;
;       [D]
;
; Bit-read order (per nesdev): A, B, Select, Start, Up, Down, Left,
; Right. The indicator_addrs table is in that order so a single
; index variable walks both arrays in sync.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG (NROM-128)
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $00             ; flag6: mapper 0, horizontal mirror
        .byte   $00             ; flag7
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
        inx                     ; X=0
        stx     $2000           ; PPUCTRL = 0
        stx     $2001           ; PPUMASK = 0 (rendering off)
        stx     $4015           ; APU silent
        lda     #$40
        sta     $4017           ; frame-counter IRQ off

        ; First vblank wait
:       bit     $2002
        bpl     :-

        ; Clear CPU RAM
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

        ; Second vblank wait
:       bit     $2002
        bpl     :-

        ; Palette: $3F00 dark blue universal, $3F01 near-white,
        ; $3F02 mid blue, $3F03 sky.
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     #$01
        sta     $2007
        lda     #$30
        sta     $2007
        lda     #$11
        sta     $2007
        lda     #$21
        sta     $2007

        ; Clear full nametable + attribute (1024 bytes).
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

        ; Initialize 8 indicator cells to empty ($30) so the user
        ; sees the layout immediately. The main loop will overwrite
        ; them once per frame anyway.
        ldx     #0
init_boxes:
        ; PPUADDR = indicator_addrs[X*2 .. X*2+1]
        txa
        asl     a               ; X * 2
        tay
        lda     indicator_addrs+1,y     ; hi byte
        sta     $2006
        lda     indicator_addrs+0,y     ; lo byte
        sta     $2006
        lda     #$30            ; empty box
        sta     $2007
        inx
        cpx     #8
        bne     init_boxes

        ; Reset scroll, enable BG show.
        lda     #$00
        sta     $2005
        sta     $2005
        lda     #$0A            ; PPUMASK bit 3 = BG show, bit 1 = BG-left
        sta     $2001

main_loop:
        ; Wait for vblank — VRAM writes must happen during vblank so
        ; the renderer doesn't see mid-frame tile updates.
:       bit     $2002
        bpl     :-

        ; Strobe joypad: pulse $4016 bit 0 high → low to latch state.
        lda     #$01
        sta     $4016
        lda     #$00
        sta     $4016

        ; For each of 8 buttons (A B Sel Sta U D L R order):
        ;   read $4016 bit 0 → A
        ;   tile = $30 + (A & 1)   ; empty (0) or full (1)
        ;   write tile to indicator_addrs[X*2..X*2+1]
        ldx     #0
read_loop:
        lda     $4016
        and     #$01            ; mask the data bit (others are open-bus)
        clc
        adc     #$30            ; $30 = empty, $31 = full
        pha                     ; stash the tile while we set PPUADDR

        txa
        asl     a
        tay
        lda     indicator_addrs+1,y
        sta     $2006
        lda     indicator_addrs+0,y
        sta     $2006
        pla
        sta     $2007

        inx
        cpx     #8
        bne     read_loop

        ; Restore scroll to (0, 0) — $2006 writes clobber the
        ; internal address latch.
        lda     #$00
        sta     $2005
        sta     $2005

        jmp     main_loop

nmi:
irq:
        rti

; Indicator nametable addresses, little-endian, in BIT-READ order:
;
;   index 0 → A      ($2199, row 12 col 25)
;   index 1 → B      ($219B, row 12 col 27)
;   index 2 → Select ($2193, row 12 col 19)
;   index 3 → Start  ($2195, row 12 col 21)
;   index 4 → Up     ($214D, row 10 col 13)
;   index 5 → Down   ($21CD, row 14 col 13)
;   index 6 → Left   ($218B, row 12 col 11)
;   index 7 → Right  ($218F, row 12 col 15)
indicator_addrs:
        .word   $2199           ; A
        .word   $219B           ; B
        .word   $2193           ; Select
        .word   $2195           ; Start
        .word   $214D           ; Up
        .word   $21CD           ; Down
        .word   $218B           ; Left
        .word   $218F           ; Right

; -------------------------------- CHR-ROM (8 KiB).
; Two tiles populate the pattern table:
;   $30 — empty box (outline only)
;   $31 — full box  (solid)
; Other tiles are blank. Indicator-only demo; no text labels.
.segment "CHARS"

        ; Tiles $00-$2F: blank
        .res    16 * $30, $00

        ; Tile $30 ('empty box'): outline
        .byte   $FF, $81, $81, $81, $81, $81, $FF, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tile $31 ('full box'): solid
        .byte   $FF, $FF, $FF, $FF, $FF, $FF, $FF, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $32-$FF: blank
        .res    16 * ($100 - $32), $00

        ; Pattern table 1 unused.
        .res    16 * 256, $00
