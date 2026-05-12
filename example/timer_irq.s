; timer_irq.s — interrupt-driven counter alongside a busy main loop.
;
; Chippy doesn't model a hardware timer chip directly, but a host
; harness (or the TUI's manual IRQ trigger) can pulse the IRQ line.
; The pattern below is what a real timer-IRQ-driven program looks
; like: main loop runs forever bumping MAIN_TICK; the IRQ handler
; bumps IRQ_TICK and returns. Both counters live in zero page so the
; TUI's watch window can pin them.
;
; Demonstrates: CLI / SEI, IRQ vector dispatch, RTI, register save /
; restore inside the handler, and how the stack panel renders the
; interrupted frame.
;
; Trigger the IRQ from chippy's TUI via the prompt:
;   :irq           — pulse the line once
; (See docs/dap.md if driving via DAP — `customRequest` style.)
;
; Watching tips:
;   :watch $50 byte "MAIN"
;   :watch $51 byte "IRQ"
;   Set the bp at irq_handler with `:bp irq_handler`. Pulse the line;
;   step through the handler; watch the I flag flip on entry.

.segment "ZEROPAGE"
MAIN_TICK = $50
IRQ_TICK  = $51

.segment "CODE"

.proc start
        cld
        ldx     #$FF
        txs

        lda     #0
        sta     MAIN_TICK
        sta     IRQ_TICK

        cli                         ; allow IRQ

main_loop:
        inc     MAIN_TICK
        jmp     main_loop
.endproc

.proc irq_handler
        pha                         ; save A — handler clobbers it
        inc     IRQ_TICK
        pla
        rti
.endproc

.segment "VECTORS"
        .word   start                ; NMI   ($FFFA) — unused
        .word   start                ; RESET ($FFFC)
        .word   irq_handler          ; IRQ   ($FFFE)
