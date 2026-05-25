; sunsoft5b-chord.s — Sunsoft 5B audio expansion probe (#325).
;
; Sunsoft FME-7 (mapper 69) cart + 5B audio half — three tone
; channels at distinct periods. Validates the v0.6 Sunsoft 5B
; path (#306) end-to-end under a headless audio-presence test.
;
; Cart-side register protocol:
;   $C000 — latch register address (R0..R15)
;   $E000 — write data to the latched register
;
; PRG layout: 16 KiB = 2 × 8 KiB banks. The four switchable
; windows ($6000/$8000/$A000/$C000) and the fixed last bank at
; $E000-$FFFF; the fixed bank carries reset + vectors.
;
; Build:  make -C roms/demos sunsoft5b-chord
; Run:    nessy roms/demos/sunsoft5b-chord/sunsoft5b-chord.nes
;
; License: MIT.

.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $51             ; flag6: mapper low nibble 5 + vertical mirror
        .byte   $40             ; flag7: mapper high nibble 4 → mapper 69 (FME-7)
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

.segment "VECTORS"
        .word   nmi
        .word   reset
        .word   irq

.segment "BANK0"
        .byte   $00             ; bank 0 unused — single pad byte

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

        ; --- Sunsoft 5B register init ---
        ; Each parameter goes via a latch-then-write sequence:
        ;   STA $C000 ; latch register index
        ;   STA $E000 ; write data

        ; R7 = mixer: bit n disables tone n (inverted). $F8 = enable
        ; tones A + B + C, disable noise channels.
        lda     #7
        sta     $C000
        lda     #$F8
        sta     $E000

        ; Tone A period (R0/R1) = $0080 → mid-audible.
        lda     #0
        sta     $C000
        lda     #$80
        sta     $E000
        lda     #1
        sta     $C000
        lda     #$00
        sta     $E000

        ; Tone B period (R2/R3) = $00A0.
        lda     #2
        sta     $C000
        lda     #$A0
        sta     $E000
        lda     #3
        sta     $C000
        lda     #$00
        sta     $E000

        ; Tone C period (R4/R5) = $00C0.
        lda     #4
        sta     $C000
        lda     #$C0
        sta     $E000
        lda     #5
        sta     $C000
        lda     #$00
        sta     $E000

        ; Tone A / B / C amplitudes (R8/R9/R10) = $0C (no envelope,
        ; fixed level 12).
        lda     #8
        sta     $C000
        lda     #$0C
        sta     $E000
        lda     #9
        sta     $C000
        lda     #$0C
        sta     $E000
        lda     #10
        sta     $C000
        lda     #$0C
        sta     $E000

        ; Spin.
:       jmp     :-

nmi:
        rti

irq:
        rti
