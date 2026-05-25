; mmc3-split.s — MMC3 scanline-IRQ split bar (#323).
;
; The screen is a flat colour (blank nametable → universal BG colour
; at $3F00). MMC3's scanline IRQ is armed to fire ~120 scanlines
; into the frame; the IRQ handler rewrites $3F00 from blue to green,
; so the top of the screen is blue + the bottom is green — a classic
; status-bar split driven entirely by the mapper's A12-counted IRQ
; (not sprite-0 hit, not cycle timing).
;
; Depends on the per-scanline A12 clock (#352): the PPU's per-line
; dummy sprite-pattern fetch toggles A12 so MMC3 counts scanlines
; even with no sprites on screen.
;
; Build:  make -C roms/demos mmc3-split
; Run:    nessy roms/demos/mmc3-split/mmc3-split.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $02             ; 2 × 16 KiB PRG (32 KiB → MMC3 4 × 8 KiB banks)
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $40             ; flag6: mapper low nibble 4 (MMC3), horizontal mirror
        .byte   $00             ; flag7: mapper high nibble 0 → mapper 4
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "VECTORS"
        .word   nmi             ; $FFFA
        .word   reset           ; $FFFC
        .word   irq             ; $FFFE

; Code + vectors live in the fixed last 8 KiB bank ($E000-$FFFF).
.segment "FIXED"

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

        ; Palette $3F00 = blue ($02) — the top-of-frame colour.
        lda     #$3F
        sta     $2006
        ldx     #$00
        stx     $2006
        lda     #$02
        sta     $2007

        ; Enable BG show. BG pattern table stays $0000 (PPUCTRL bit 4
        ; clear) so the per-line A12 dummy fetch produces a clean rise.
        lda     #$08
        sta     $2001

        ; Arm MMC3 IRQ: fire 120 scanlines into the frame.
        lda     #120
        sta     $C000           ; IRQ latch
        lda     #$00
        sta     $C001           ; reload (counter reloads from latch)
        sta     $E001           ; IRQ enable

        ; Enable NMI on vblank ($2000 bit 7). BG pattern table = $0000.
        lda     #$80
        sta     $2000
        cli
:       jmp     :-

; IRQ: mid-frame split. Rewrite $3F00 = green ($1A), ack + re-enable
; the MMC3 IRQ for the next frame.
irq:
        pha
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     #$1A            ; green
        sta     $2007
        ; Ack + re-enable.
        sta     $E000           ; disable + acknowledge
        sta     $E001           ; re-enable
        ; Reset scroll latch (the $2006 writes moved the address).
        lda     #$00
        sta     $2005
        sta     $2005
        pla
        rti

; NMI: top of frame. Restore $3F00 = blue for the top region + reload
; the IRQ counter so the split fires again next frame.
nmi:
        pha
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     #$02            ; blue
        sta     $2007
        lda     #$00
        sta     $C001           ; reload IRQ counter from latch
        lda     #$00
        sta     $2005
        sta     $2005
        pla
        rti

.segment "CHARS"
        ; tile $00: blank (transparent → universal bg colour). Rest of
        ; CHR zero-filled by the cfg.
        .res    16
