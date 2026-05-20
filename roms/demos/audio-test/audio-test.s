; audio-test.s — nessy's first audio demo (#193 follow-up to APU work).
;
; Cycles a 12-note chromatic scale A4..G#5 on pulse-1 channel,
; advancing one note every ~0.5 s (driven by the NMI handler). 50%
; duty, length-halt set + constant volume so each note sustains
; cleanly until the next $4003 write. Audible across all common
; sample-rate / mixer paths.
;
; Build:  make -C roms/demos audio-test
; Run:    nessy roms/demos/audio-test/audio-test.nes
;
; The screen stays black — this demo is audio-only. Volume is fixed
; at 7/15 (a moderate, ear-safe level).
;
; License: MIT (chippy's license — this ROM is original work).

; -------------------------------- iNES header
.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG (NROM-128)
        .byte   $01             ; 1 ×  8 KiB CHR
        .byte   $00             ; flag6: mapper 0 (NROM), horizontal mirror
        .byte   $00             ; flag7
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

; -------------------------------- reset / NMI / IRQ vectors
.segment "VECTORS"
        .word   nmi             ; $FFFA
        .word   reset           ; $FFFC
        .word   irq             ; $FFFE

; -------------------------------- zero-page state
.segment "ZEROPAGE"
note_idx:       .res 1          ; 0..11 — index into note_lo / note_hi
frame_cnt:      .res 1          ; counts vblank IRQs since last note change

.segment "CODE"

; reset — power-on entry. Standard 2-vblank wait so the PPU is in a
; known state, then configure pulse 1 and enable the NMI line so the
; frame-counter NMI can drive note advancement.
reset:
        sei
        cld
        ldx     #$FF
        txs
        inx                     ; X = 0
        stx     $2000           ; PPUCTRL = 0 (NMI off for now)
        stx     $2001           ; PPUMASK = 0
        stx     $4015           ; disable all channels first
        lda     #$40
        sta     $4017           ; frame-counter IRQ off, 4-step mode

        ; Wait for two vblanks (PPU warmup).
:       bit     $2002
        bpl     :-
:       bit     $2002
        bpl     :-

        ; Enable pulse 1.
        lda     #$01
        sta     $4015

        ; Configure pulse 1: duty 50% (bits 6-7 = 10), halt (bit 5 = 1),
        ; constant volume (bit 4 = 1), volume 7 (bits 0-3 = 0111).
        ; Binary: 1011_0111 = $B7.
        lda     #$B7
        sta     $4000

        ; Sweep off.
        lda     #$00
        sta     $4001

        ; Init state + play first note.
        lda     #$00
        sta     note_idx
        sta     frame_cnt
        jsr     play_current

        ; Enable NMI line so the PPU's vblank fires the handler.
        lda     #$80
        sta     $2000

        ; Idle forever.
forever:
        jmp     forever

; NMI handler — fires once per frame (60 Hz NTSC). Every 30 frames
; (~0.5 s) advance to the next note in the chromatic scale.
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
        cmp     #12
        bcc     :+
        lda     #$00
        sta     note_idx
:       jsr     play_current
done:
        pla
        tax
        pla
        rti

; play_current — load the timer period for note_idx into pulse 1's
; period registers. The high byte gets OR'd with $08 to set the
; length-counter index (any non-zero is fine; lengthHalt is already
; set so the counter doesn't drain).
play_current:
        ldx     note_idx
        lda     note_lo,x
        sta     $4002
        lda     note_hi,x
        ora     #$08
        sta     $4003
        rts

; IRQ handler — unused; $4017 frame-counter IRQ is masked above.
irq:
        rti

; Period values for the 12-note chromatic scale A4..G#5 on the NES's
; 1.789773 MHz CPU clock. f = CPU / (16 * (period + 1)).
;
;   A4 440.00 Hz → period 253
;   A#4 466.16   → 239
;   B4  493.88   → 225
;   C5  523.25   → 213
;   C#5 554.37   → 200
;   D5  587.33   → 189
;   D#5 622.25   → 179
;   E5  659.26   → 169
;   F5  698.46   → 159
;   F#5 739.99   → 150
;   G5  783.99   → 142
;   G#5 830.61   → 134
note_lo:
        .byte   253, 239, 225, 213, 200, 189, 179, 169, 159, 150, 142, 134
note_hi:
        .byte     0,   0,   0,   0,   0,   0,   0,   0,   0,   0,   0,   0
