; vrc6-chord.s — VRC6 audio expansion probe (#324).
;
; Konami VRC6 (mapper 24) cart that programs all three audio
; channels (2 pulse + 1 sawtooth) at distinct frequencies and
; holds them. Validates the v0.6 VRC6 audio path (#302) end-to-
; end under a headless audio-presence test.
;
; Audible: a sustained low chord. Visible: black screen.
;
; PRG layout — 16 KiB total = 2 × 8 KiB banks. The 16 KiB
; switchable window at $8000-$BFFF maps to bank 0 (unused);
; the fixed last bank at $E000-$FFFF holds code + vectors.
;
; Build:  make -C roms/demos vrc6-chord
; Run:    nessy roms/demos/vrc6-chord/vrc6-chord.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $81             ; flag6: mapper low nibble 8 + vertical mirror (bit 0)
        .byte   $10             ; flag7: mapper high nibble 1 → mapper 24 (VRC6a)
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "VECTORS"
        .word   nmi
        .word   reset
        .word   irq

.segment "BANK0"
        .byte   $00             ; pad so cfg sees a non-empty bank

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

        ; Set mirroring + banking control via $B003. Bits 2-3
        ; pick the scheme; we go vertical (0).
        lda     #$00
        sta     $B003

        ; Pulse 1: volume 15, duty 4 (50%-ish), period $0200.
        lda     #$F4
        sta     $9000
        lda     #$00
        sta     $9001
        lda     #$82            ; period high $2 + enable bit 7
        sta     $9002

        ; Pulse 2: volume 15, duty 2 (25%), period $0300.
        lda     #$F2
        sta     $A000
        lda     #$00
        sta     $A001
        lda     #$83
        sta     $A002

        ; Sawtooth: rate $20, period $0400.
        lda     #$20
        sta     $B000
        lda     #$00
        sta     $B001
        lda     #$84
        sta     $B002

        ; Spin — VariantNES skips the JMP-self halt heuristic.
:       jmp     :-

nmi:
        rti

irq:
        rti
