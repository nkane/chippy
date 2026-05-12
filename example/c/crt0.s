; C runtime startup for chippy.
;
; cc65 emits position-independent C code that expects a small amount of
; bring-up before main() runs: hardware stack initialised, soft stack
; pointer set to the top of available RAM, BSS cleared, initialised
; DATA copied from its ROM image to its RAM-resident run address, and
; cc65's constructor table (if any) invoked.
;
; After main() returns the CPU sits in a JMP-self halt that chippy's
; Step() recognises and reports as "halted at $XXXX".

.export   _exit
.export   __STARTUP__: absolute = 1

.import   _main
.import   zerobss, copydata, initlib, donelib
.import   __RAM_START__, __RAM_SIZE__, __STACKSIZE__

.importzp sp

.segment "STARTUP"

start:
    sei                     ; mask IRQs during bring-up
    cld                     ; cc65 requires binary mode
    ldx     #$ff
    txs                     ; hardware stack to $01FF

    ; Point cc65's soft stack at the top of RAM (grows downward).
    lda     #<(__RAM_START__ + __RAM_SIZE__)
    sta     sp
    lda     #>(__RAM_START__ + __RAM_SIZE__)
    sta     sp+1

    jsr     zerobss         ; zero the BSS segment
    jsr     copydata        ; load DATA from its ROM image to RAM
    jsr     initlib         ; run any C++-style constructors (rarely used)

    cli                     ; re-enable IRQs before user code
    jsr     _main           ; into your C entry point

_exit:
    jsr     donelib         ; constructor counterparts
halt:
    jmp     halt            ; chippy detects PC==prevPC and halts the loop

.segment "VECTORS"

.word   start               ; NMI  ($FFFA)
.word   start               ; RESET ($FFFC)
.word   start               ; IRQ  ($FFFE) — TODO: route to a user handler
