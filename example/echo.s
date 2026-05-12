; echo.s — Apple-1-style keyboard echo. Reads $F004 / $F005, writes
; to $F001. Type into chippy's TUI input mode (`i`) and watch the
; characters come back to the TextOutput pane.
;
; Demonstrates: MMIO peripherals, polling I/O, register-flag testing,
; the JSR/RTS call boundary (annotated in the TUI's stack panel).
;
; Watching tips:
;   :watch $F004 byte "KBD"
;   :watch $F005 byte "KBDCR"
;   Set a breakpoint at putc with `:bp putc`. Press `i`, type a key,
;   `c` (continue) — the bp fires the moment a key drains the latch.

KBD_DATA   = $F004
KBD_STATUS = $F005
TXT_OUT    = $F001

.segment "CODE"

.proc start
        ; CLD: NMOS-and-CMOS-safe entry; we don't use decimal mode.
        cld
        ldx     #$FF
        txs

main_loop:
        jsr     getc
        jsr     putc
        jmp     main_loop

.endproc

; getc: poll KBD_STATUS until bit 7 set, then read KBD_DATA. Returns
; the ASCII byte in A (bit 7 already cleared by KeyboardInput.Read).
.proc getc
wait:   lda     KBD_STATUS
        bpl     wait                ; bit 7 = 0 → no key yet
        lda     KBD_DATA
        and     #$7F                ; Apple-1 returns key | $80; mask off
        rts
.endproc

; putc: write A to TXT_OUT. CR ($0D) is translated to LF by chippy's
; TextOutput peripheral so successive lines render cleanly in the
; output pane.
.proc putc
        sta     TXT_OUT
        rts
.endproc

.segment "VECTORS"
        .word   start
        .word   start
        .word   start
