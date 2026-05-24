; mmc1-banks.s — MMC1 PRG bank-switching demo (#261).
;
; Two 16 KiB PRG banks. The switchable bank at $8000-$BFFF holds a
; single data byte: the universal-background palette color this
; bank "owns". The fixed bank at $C000-$FFFF holds reset / NMI
; code that reads $8000, writes it to $3F00, then toggles the
; active bank every ~30 frames via the standard MMC1 5-write
; serial protocol to $E000 (prgBank register in PRG mode 3).
;
; Visible behaviour: background flashes between two colours roughly
; twice per second. Headless test asserts framebuffer differs
; between frames 5 and 60.
;
; Validates the v0.4 MMC1 mapper (#248) end-to-end: serial-shift
; reset (bit 7 write), 5-write commit, prgBank register selecting
; which 16 KiB block lives at $8000-$BFFF, and fixed-last-bank
; mode driving the reset / NMI vectors.
;
; Build:  make -C roms/demos mmc1-banks
; Run:    nessy roms/demos/mmc1-banks/mmc1-banks.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $02             ; 2 × 16 KiB PRG
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $10             ; mapper 1, horizontal mirroring
        .byte   $00
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "ZEROPAGE"
frame_cnt:      .res 1
bank_idx:       .res 1

; ===========================================================================
; Bank 0 — switchable. Lives at $8000-$BFFF when prgBank=0.
; First byte is the palette-color byte the fixed bank reads + writes
; to $3F00.
; ===========================================================================
.segment "BANK0"
        .byte   $0F             ; black (NES palette index $0F)
        ; pad to 16 KiB — automatic via cfg fillval.

; ===========================================================================
; Bank 1 — switchable. Lives at $8000-$BFFF when prgBank=1.
; ===========================================================================
.segment "BANK1"
        .byte   $30             ; white (NES palette index $30)

; ===========================================================================
; Fixed bank — last 16 KiB. Lives at $C000-$FFFF, contains the
; reset / NMI handlers + the reset / NMI / IRQ vectors at $FFFA-$FFFF.
; ===========================================================================
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

        ; Reset MMC1 shift register. Any write with bit 7 set clears
        ; the internal accumulator + ORs $0C into the control reg
        ; (forces PRG mode 3 = fix-last). After this we're guaranteed
        ; to be in the layout the linker expects.
        lda     #$80
        sta     $8000

        ; Palette setup: white text on black, plus universal BG color
        ; will be overwritten every NMI from the active bank's byte.
        lda     #$3F
        sta     $2006
        lda     #$00
        sta     $2006
        lda     #$0F            ; placeholder; NMI overwrites
        sta     $2007

        ; Enable BG show. Sprites stay off — pure bg-color demo.
        lda     #$08
        sta     $2001

        ; Start at bank 0.
        lda     #$00
        sta     bank_idx
        jsr     select_bank

        ; NMI carries the action from here on.
        lda     #$80
        sta     $2000           ; enable NMI on vblank
        cli
:       jmp     :-

; select_bank: A = target bank (0 or 1). MMC1 5-write protocol to
; $E000 (the prgBank register in PRG mode 3). Each store shifts
; bit 0 of the operand into the internal 5-bit shift register;
; on the fifth store the assembled value commits.
select_bank:
        ldx     #5
@loop:
        sta     $E000
        lsr     a
        dex
        bne     @loop
        rts

nmi:
        pha
        txa
        pha
        tya
        pha

        ; Read this bank's color byte ($8000) and stash it at $3F00.
        lda     $8000
        ldx     #$3F
        stx     $2006
        ldx     #$00
        stx     $2006
        sta     $2007

        ; Reset scroll latch so the BG palette write doesn't bias
        ; rendering.
        lda     #$00
        sta     $2005
        sta     $2005

        ; Every 30 frames, toggle bank.
        inc     frame_cnt
        lda     frame_cnt
        cmp     #30
        bcc     @done
        lda     #0
        sta     frame_cnt
        lda     bank_idx
        eor     #1
        sta     bank_idx
        jsr     select_bank

@done:
        pla
        tay
        pla
        tax
        pla
        rti

irq:
        rti

.segment "VECTORS"
        .word   nmi             ; $FFFA
        .word   reset           ; $FFFC
        .word   irq             ; $FFFE
