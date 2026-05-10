; cmos_demo.s — exercises 65C02-only opcodes: BRA, STZ, PHX, PHY, INC A.
;
; Build with cc65 targeting 65C02:
;   ca65 --cpu 65c02 -g cmos_demo.s -o cmos_demo.o
;   ld65 -C load_five.cfg -o cmos_demo.bin --dbgfile cmos_demo.dbg cmos_demo.o
;
; Run in chippy:
;   chippy --cpu 65c02 -rom example/cmos_demo.bin

.setcpu "65c02"
.segment "CODE"

.proc start
        lda     #$AA
        ldx     #$BB
        ldy     #$CC
        phx                     ; push X ($BB)
        phy                     ; push Y ($CC)
        stz     $00             ; zero $0000 (CMOS)
        lda     #$10
        inc     a               ; CMOS INC A -> $11
        bra     halt            ; CMOS unconditional branch
        nop
halt:   jmp     halt
.endproc

.segment "VECTORS"
        .word   start
        .word   start
        .word   start
