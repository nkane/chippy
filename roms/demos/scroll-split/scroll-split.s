; scroll-split.s — mid-frame horizontal scroll split (#328).
;
; Background is vertical 8px stripes (alternating blank / solid
; columns). The main loop sets scroll-X = 0 at the top of the
; visible frame, busy-waits ~half a frame, then rewrites scroll-X
; via $2005 mid-render. The PPU's per-scanline renderer (#206)
; captures the mid-frame write, so the top half draws at scroll 0
; and the bottom half at the new scroll — a visible split.
;
; Timing is cycle-counted (not sprite-0-hit), which is deterministic
; under nessy's fixed cycle model, so the framebuffer SHA is stable
; even though the exact split scanline isn't hardware-pinned.
;
; Build:  make -C roms/demos scroll-split
; Run:    nessy roms/demos/scroll-split/scroll-split.nes
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
vblank_flag:    .res 1

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

        ; Palette: $3F00 = black, $3F01 = white.
        lda     #$3F
        sta     $2006
        ldx     #$00
        stx     $2006
        lda     #$0F
        sta     $2007           ; universal bg black
        lda     #$30
        sta     $2007           ; BG[1] white

        ; Fill nametable 0: even columns = tile $01 (solid), odd = $00.
        ; 30 rows × 32 cols. $2006 = $2000.
        lda     #$20
        sta     $2006
        lda     #$00
        sta     $2006
        ldy     #30             ; rows
fill_row:
        ldx     #0              ; col 0..31
fill_col:
        txa
        and     #$01            ; even col → 0 → tile $01; odd → tile $00
        eor     #$01            ; even → 1 (tile $01), odd → 0 (tile $00)
        sta     $2007
        inx
        cpx     #32
        bne     fill_col
        dey
        bne     fill_row

        ; Enable BG show.
        lda     #$08
        sta     $2001
        ; Enable NMI on vblank.
        lda     #$80
        sta     $2000
        cli

main:
        ; Wait for the NMI (start of vblank → next visible frame).
        lda     #0
        sta     vblank_flag
:       lda     vblank_flag
        beq     :-

        ; Top of frame: scroll-X = 0.
        lda     #$00
        sta     $2005           ; scroll X
        sta     $2005           ; scroll Y
        ; (also need $2000 nametable bits stable — left at NMI-enable $80)

        ; Busy-wait ~half a frame so the next scroll write lands
        ; mid-render. ~13000 CPU cycles ≈ scanline 120. Nested loop:
        ; inner 256×~5 = ~1280 cyc; outer 10 → ~12800 cyc.
        ldx     #10
wait_outer:
        ldy     #0
wait_inner:
        dey
        bne     wait_inner
        dex
        bne     wait_outer

        ; Mid-frame: shift scroll-X by 8 (one full stripe). The
        ; per-scanline renderer applies this from here down.
        lda     #$08
        sta     $2005
        lda     #$00
        sta     $2005
        jmp     main

nmi:
        lda     #1
        sta     vblank_flag
        rti

irq:
        rti

.segment "CHARS"
        ; tile $00: blank (transparent → universal bg black).
        .res    16
        ; tile $01: solid (both planes $FF → palette index 3, but we
        ; use BG[1] so low plane $FF / high plane $00 → index 1 white).
        .repeat 8
        .byte   $FF
        .endrepeat
        .repeat 8
        .byte   $00
        .endrepeat
