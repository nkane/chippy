# chippy example programs

Five small ca65/cc65 programs, all linked against the shared
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

All programs spin on a `JMP halt` so the TUI keeps showing the final state.

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
