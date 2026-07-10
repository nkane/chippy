# chippy example programs

Eleven small ca65 programs, all linked against the shared
`load_five.cfg` (ROM at `$8000-$FFFF`, reset vector at `$FFFC`). Each one is
self-contained — assemble, then load the resulting `.bin` in chippy.

## Build

Requires [cc65](https://cc65.github.io/) on `$PATH`.

```sh
make            # builds every .bin + .dbg
make run        # builds + runs load_five.bin in chippy
make run-fibonacci   # or run-count_to_ten / run-stack_demo / run-bcd_add
make clean
```

## Programs

| Program        | Demonstrates                                   | Final state                              |
|----------------|------------------------------------------------|------------------------------------------|
| `load_five`    | Immediate loads into A / X / Y                 | A=X=Y=`$05`                              |
| `count_to_ten` | INX loop with `CPX` / `BNE`                    | X=`$0A`, Z=1                             |
| `fibonacci`    | Zero-page state, ADC carry-out, indexed store  | 13 bytes at `$0200..$020C`, X=`$0D`      |
| `stack_demo`   | `PHA` / `PLA` order, register transfers        | A=`$11`, X=`$33`, Y=`$22`, SP=`$FF`      |
| `bcd_add`      | `SED` + `ADC` decimal-mode arithmetic          | A=`$07`, X=`$01` (BCD: 49 + 58 = 107)    |
| `mul16`        | 16x16 → 32-bit shift-add multiply; ZP state    | $0050..0053 = `$06260060` ($1234 * $5678)|
| `echo`         | Apple-1 I/O — poll $F005, read $F004, write $F001 | runs forever; type in TUI input mode  |
| `timer_irq`    | IRQ vector + RTI alongside a busy main loop    | MAIN_TICK + IRQ_TICK in zero page        |
| `guess`        | Interactive state machine driven by keyboard   | prints `<` / `>` / `!` per guess         |
| `hello`        | Apple-1 putc loop — write a string to $F001    | "HELLO WORLD\n" in the output pane       |
| `cmos_demo`    | 65C02-only opcodes (BBR/STZ/BRA/etc.)          | requires `-cpu 65c02`                    |
| `serial_echo`  | 6551 ACIA serial echo — poll RDRF/TDRE at $5000 | `-acia '$5000'`; type in input mode      |

All programs spin on a `JMP halt` (or an infinite poll loop) so the TUI keeps showing live state.

`serial_echo` is built by a Go generator rather than the ca65 Makefile — it emits a full
$8000–$FFFF image (reset/IRQ vectors included) and self-verifies the echo on chippy's own
core. Build it, then run with the ACIA wired:

```
go run example/gen_serial_echo.go            # writes example/serial_echo.bin
chippy -rom example/serial_echo.bin -acia '$5000'
```

then press `i` and type — each key echoes into the Serial pane.

## Categorized

| Category       | Try first             | Then                                |
|----------------|-----------------------|-------------------------------------|
| Basic CPU      | `load_five`           | `count_to_ten`, `stack_demo`        |
| Arithmetic     | `fibonacci`           | `mul16`, `bcd_add`                  |
| I/O            | `hello`               | `echo`, `guess`, `serial_echo`      |
| Interrupts     | `timer_irq`           | —                                   |
| CMOS-only      | `cmos_demo`           | —                                   |

## Suggested chippy session

Try fibonacci with watchpoints on the result table:

```
:bpw $0200 log fib[0]={[$0200]}
:watch $0200 byte fib0
:watch $0201 byte fib1
:watch $0202 byte fib2
r
```

Or watch BCD math step by step:

```
chippy -rom bcd_add.bin
:bp start
r
s s s s s s s s s s s s
```

## Layout

All programs share `load_five.cfg`:

```
MEMORY  ROM: $8000 size $8000 fill $00
SEGMENT CODE   @ $8000
SEGMENT VECTORS @ $FFFA  (NMI / RESET / IRQ words)
```

Add a new demo by dropping `myprog.s` next to the others and adding
`myprog` to the `PROGRAMS` list in the `Makefile`.
