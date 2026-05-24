; dmc-sample.s — DMC delta-PCM playback demo (#260).
;
; Configures the DMC channel to loop a 65-byte alternating-bit
; sample (.byte $AA, $55, ...) at rate index 0 (~33.1 KHz bit
; rate on NTSC). The result is a sustained low-frequency buzz
; produced by the delta-PCM toggling the 7-bit output between
; +2 / -2 on each bit. Loop bit set so the sample restarts forever;
; IRQ enable cleared (no DMC IRQ fires during playback).
;
; Sample placement: $F000 inside the PRG ROM via a dedicated
; SAMPLE linker segment so the DMA fetch can read it without
; collision with code. $4012 sets address = ($F000-$C000)/64 = $C0;
; $4013 sets length = (65-1)/16 = 4 → fetch 65 bytes per loop.
;
; Validates the v0.3 DMC pipeline end-to-end (#246): DMA stall
; charged on byte fetch, delta-encoded bit stream advances the
; 7-bit level, loop bit reloads sample on exhaustion.
;
; Build:  make -C roms/demos dmc-sample
; Run:    nessy roms/demos/dmc-sample/dmc-sample.nes
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
        stx     $4015           ; silence all channels at boot
        lda     #$40
        sta     $4017           ; 4-step mode, IRQ inhibit

        ; Two-vblank warmup so the PPU has stabilized before we
        ; do anything else. Standard NES boot ritual.
:       bit     $2002
        bpl     :-
:       bit     $2002
        bpl     :-

        ; --- DMC configuration ---
        ; $4010: bit 7 = IRQ enable (0), bit 6 = loop (1),
        ;        bits 0-3 = rate index (0 = 428 CPU cycles/bit on NTSC).
        lda     #$40
        sta     $4010

        ; $4011: direct 7-bit output level (mid = $40).
        lda     #$40
        sta     $4011

        ; $4012: sample address = $C000 + (v * 64). v=$C0 → $F000.
        lda     #$C0
        sta     $4012

        ; $4013: sample length = (v * 16) + 1. v=4 → 65 bytes.
        lda     #$04
        sta     $4013

        ; Enable DMC ($4015 bit 4). Bytes-remaining > 0 triggers
        ; the first DMA fetch on the next APU step.
        lda     #$10
        sta     $4015

        ; Idle. The NES halt heuristic is gated off for VariantNES
        ; (#194) so this loop ticks the bus indefinitely; DMC keeps
        ; looping audio.
self:   jmp     self

nmi:    rti

irq:    rti

; The DMC sample lives in its own 4 KiB-aligned segment. Linker
; cfg places SAMPLE at $F000. 65 bytes (the smallest legal DMC
; length) of alternating $AA / $55 — the bitstream is
; 10101010 01010101 …, which after delta-PCM expansion produces
; a square-wave-shaped output around the $40 midpoint.
.segment "SAMPLE"
        .repeat 33
        .byte   $AA
        .endrepeat
        .repeat 32
        .byte   $55
        .endrepeat
