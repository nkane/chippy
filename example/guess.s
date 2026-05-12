; guess.s — interactive "guess the number" game.
;
; The reference number lives in zero page (NUMBER at $40); the user
; types digit guesses through chippy's keyboard ($F004). A guess is a
; single ASCII digit. The program responds via TextOutput:
;   '<'  guess too low
;   '>'  guess too high
;   '!'  match (and halts)
; Anything that isn't a digit prints '?' and waits for the next keystroke.
;
; Demonstrates: a small state machine driven from MMIO, table-driven
; comparison, conditional jumps for early-exit, a labelled write-error
; path. Also a good target for stepping with `f` (run-to-next-line) so
; the TUI source view stays in step with the dispatch logic.
;
; Suggested chippy session:
;   chippy -rom example/guess.bin
;   `i` to enter input mode, type digits, ESC to exit input mode,
;   `r` to keep running between guesses.
;
; Watching tips:
;   :watch $40 byte "TARGET"
;   :watch $41 byte "LAST_GUESS"
;   :bp check_guess if A == TARGET
;     (Stops only on a winning guess — exercise of conditional bps.)

.segment "ZEROPAGE"
NUMBER     = $40
LAST_GUESS = $41

KBD_DATA   = $F004
KBD_STATUS = $F005
TXT_OUT    = $F001

.segment "CODE"

.proc start
        cld
        ldx     #$FF
        txs
        lda     #'7'                  ; reference: ASCII '7'
        sta     NUMBER

        lda     #'?'                  ; prompt the user once
        sta     TXT_OUT

loop:
        jsr     getc
        sta     LAST_GUESS

        ; Reject non-digits ('0'..'9' = $30..$39).
        cmp     #'0'
        bcc     not_digit
        cmp     #':'
        bcs     not_digit

check_guess:
        cmp     NUMBER
        beq     win
        bcc     too_low
        ; fallthrough → too_high

too_high:
        lda     #'>'
        sta     TXT_OUT
        jmp     loop

too_low:
        lda     #'<'
        sta     TXT_OUT
        jmp     loop

not_digit:
        lda     #'?'
        sta     TXT_OUT
        jmp     loop

win:
        lda     #'!'
        sta     TXT_OUT
halt:   jmp     halt
.endproc

.proc getc
wait:   lda     KBD_STATUS
        bpl     wait
        lda     KBD_DATA
        and     #$7F
        rts
.endproc

.segment "VECTORS"
        .word   start
        .word   start
        .word   start
