; hello-bg.s — nessy's first homemade demo (issue #194 / epic #193).
;
; Static title screen rendering "HELLO NESSY" centered on the
; nametable. No animation, no input. Exercises the iNES → cart →
; MMIO → CPU + PPU integration end-to-end with a real hand-rolled
; ROM, doubling as a v0.1 regression test (the framebuffer SHA is
; pinned in cmd/nessy/demo_test.go).
;
; Build:  make -C roms/demos hello-bg
; Run:    nessy roms/demos/hello-bg/hello-bg.nes
;
; License: MIT (chippy's license — this ROM is original work).

; -------------------------------- iNES header
.segment "HEADER"
        .byte   "NES", $1A
        .byte   $01             ; 1 × 16 KiB PRG bank (NROM-128)
        .byte   $01             ; 1 ×  8 KiB CHR bank
        .byte   $00             ; flag6: mapper 0 (NROM), horizontal mirror
        .byte   $00             ; flag7
        .byte   $00,$00,$00,$00,$00,$00,$00,$00

; -------------------------------- reset / NMI / IRQ vectors
.segment "VECTORS"
        .word   nmi             ; $FFFA
        .word   reset           ; $FFFC
        .word   irq             ; $FFFE

; -------------------------------- program code
.segment "CODE"

; reset — power-on entry. Standard 2-vblank PPU warmup, then write
; palette + nametable + enable background rendering, then spin.
reset:
        sei
        cld                     ; 2A03 has no decimal adder; ignored, but
                                ;   conventional in real NES code.
        ldx     #$FF
        txs                     ; init stack pointer
        inx                     ; X = 0
        stx     $2000           ; PPUCTRL = 0 (NMI off, default tables)
        stx     $2001           ; PPUMASK = 0 (rendering off)
        stx     $4015           ; APU disable (write OK even though APU
                                ;   isn't modeled — silent until v0.2)
        lda     #$40
        sta     $4017           ; frame-counter IRQ off

        ; Wait for first vblank
:       bit     $2002
        bpl     :-

        ; Clear $0000-$07FF (CPU internal 2 KiB RAM). Standard hygiene
        ; even though this demo doesn't use RAM — keeps tests
        ; deterministic if the CPU ever boots with garbage RAM.
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

        ; Wait for second vblank (PPU stable per nesdev convention)
:       bit     $2002
        bpl     :-

        ; Palette: $3F00 first universal bg, then sub-palette 0
        ;   $3F00 = $01  dark blue   (universal background)
        ;   $3F01 = $30  near-white  (palette color 1, used for the text)
        ;   $3F02 = $11  blue accent (unused — reserved for variety)
        ;   $3F03 = $21  sky blue    (unused)
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

        ; Fill nametable + attribute table with tile $00 (blank).
        ; PPUADDR = $2000, then 4 × 256 = 1024 bytes covers the
        ; full nametable (960) + attribute (64) of bank 0.
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

        ; Write "HELLO NESSY" centered on row 14 (zero-indexed).
        ; Nametable addr = $2000 + 14*32 + ((32-11)/2) = $21CA.
        lda     #$21
        sta     $2006
        lda     #$CA
        sta     $2006
        ldx     #0
write_str:
        lda     hello_str,x
        beq     done_str        ; null terminator → done
        sta     $2007
        inx
        bne     write_str
done_str:

        ; Reset scroll to (0, 0). Two writes to $2005 clears the
        ; internal fine-X / coarse-X latch.
        lda     #$00
        sta     $2005
        sta     $2005

        ; Enable BG show. PPUMASK bit 3 = show BG, bit 1 = show BG in
        ; leftmost 8 pixels (cosmetic, but typical).
        lda     #$0A
        sta     $2001

forever:
        jmp     forever

; nmi / irq: present so the vector table is non-zero; no-op for this
; demo since NMI is disabled in PPUCTRL.
nmi:
irq:
        rti

; -------------------------------- read-only data
hello_str:
        .byte   "HELLO NESSY", 0

; -------------------------------- CHR-ROM (8 KiB).
; Pattern table 0 ($0000-$0FFF) holds the 256 tiles indexed by the
; CPU when it writes a nametable byte. We populate exactly the tiles
; "HELLO NESSY " needs (indexed by their ASCII codes) and leave the
; rest blank.
;
; Each tile is 16 bytes: 8 bytes of low-plane (bit i = LSB of pixel
; column 7-i) followed by 8 bytes of high-plane (same shape, MSB).
; We only use palette color 1 (value 1 = low bit set, high bit clear),
; so all high-plane bytes are zero.
.segment "CHARS"

        ; Tiles $00-$1F: blank ($1F = 31 tiles).
        .res    16*32, $00

        ; Tile $20 (' '): blank — same as $00 but kept distinct so
        ; the nametable writer can index by ASCII without translation.
        .res    16, $00

        ; Tiles $21-$44: blank (we don't render any of these characters).
        .res    16 * ($45 - $21), $00

        ; Tile $45 ('E')
        .byte   $FE, $80, $80, $FC, $80, $80, $FE, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $46-$47: blank
        .res    16 * 2, $00

        ; Tile $48 ('H')
        .byte   $81, $81, $81, $FF, $81, $81, $81, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $49-$4B: blank
        .res    16 * 3, $00

        ; Tile $4C ('L')
        .byte   $80, $80, $80, $80, $80, $80, $FE, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tile $4D: blank
        .res    16, $00

        ; Tile $4E ('N')
        .byte   $81, $C1, $A1, $91, $89, $85, $83, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tile $4F ('O')
        .byte   $7E, $81, $81, $81, $81, $81, $7E, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $50-$52: blank
        .res    16 * 3, $00

        ; Tile $53 ('S')
        .byte   $7E, $80, $80, $7C, $02, $02, $7C, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $54-$58: blank
        .res    16 * 5, $00

        ; Tile $59 ('Y')
        .byte   $81, $81, $42, $24, $18, $18, $18, $00
        .byte   $00, $00, $00, $00, $00, $00, $00, $00

        ; Tiles $5A-$FF: blank
        .res    16 * ($100 - $5A), $00

        ; Pattern table 1 ($1000-$1FFF): unused, all blank.
        .res    16 * 256, $00
