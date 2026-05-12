# C example programs

Three small C programs that compile through `cc65 → ca65 → ld65` and run
on chippy:

| File          | What it does                                          |
|---------------|-------------------------------------------------------|
| `hello.c`     | Writes a greeting to the TextOutput peripheral (`$F001`). |
| `sum.c`       | Computes `1+2+…+10` and stores `$37` at `$0200`.      |
| `fizzbuzz.c`  | FizzBuzz 1..15, output to `$F001`.                    |

Plus the supporting infrastructure:

| File          | Role                                                  |
|---------------|-------------------------------------------------------|
| `chippy.cfg`  | ld65 linker config (ROM `$8000-$FFF9`, RAM `$0200-$7FFF`, vectors `$FFFA-$FFFF`). |
| `crt0.s`      | Minimal C runtime: stack init, `zerobss`/`copydata`, `jsr _main`, JMP-self halt. |
| `Makefile`    | Pipeline: `cc65 -t none -g -O` → `ca65` → `ld65 -C chippy.cfg ... none.lib`. |

## Build

Needs the `cc65` toolchain on PATH (`brew install cc65` on macOS,
`apt install cc65` on Debian/Ubuntu).

```sh
make            # build every .bin
make hello.bin  # build a single program
make clean      # remove .bin / .o / .s / .dbg artifacts
```

## Run + step through C source

```sh
chippy -rom hello.bin
```

The CPU starts paused at reset — that's `crt0._start`, **not** your
`main`. Pressing `v` immediately shows `crt0.s` because chippy is
honestly reporting the file for the current PC.

To actually step C code:

```
:bp main          set a breakpoint at main (no underscore — chippy
                  strips cc65's leading `_`)
r                 run; hits the breakpoint when crt0 finishes setup
v                 toggle source view → now shows hello.c / sum.c / etc.
f                 run to next source line (the natural C-stepping key)
n                 step over (skips into cc65 runtime helpers like
                  pusha, copydata, etc. — invisible C-level)
s                 single-step at the asm level (useful inside helpers)
```

Other helpful commands:

```
:bp <symbol>      break at any C function (chippy_putc, put_u8, ...)
:bpw $0200        watch writes to a memory location
:watch reg A      pin register A in the watch panel
:goto $0200       scroll the memory view (read sum.c's result there)
```

## How the .dbg keeps C-source-stepping working

cc65's `-g` flag emits both:

- `.s` line records — addresses → cc65's generated assembly intermediate
- `.c` line records — addresses → the original C source

chippy's source-map loader (`internal/symbols`) prefers C / header files
when both records map to the same PC, so `v` shows your `.c` file. The
fallback to `.s` only kicks in for addresses with no C mapping — i.e.
the crt0 prologue, cc65 runtime stubs (`pusha`, `popax`, `copydata`),
and other generated glue. Stepping with `f` (run-to-next-source-line)
naturally skips over those.

## Memory map

```
$0000-$001F  ZP             cc65 zero-page (soft stack pointer)
$0020-$00FF  ZP user        free for inline asm
$0100-$01FF  Hardware stack 6502 push/pull, JSR/RTS
$0200-$7FFF  RAM            BSS, DATA (copied from ROM), cc65 soft stack
$8000-$FFF9  ROM            CODE + RODATA + DATA ROM image + crt0
$FFFA-$FFFB  NMI vector     → crt0.start
$FFFC-$FFFD  RESET vector   → crt0.start (this is what chippy uses)
$FFFE-$FFFF  IRQ vector     → crt0.start (no user IRQ handler in v1)
```

Override the stack size with `__STACKSIZE__` in `chippy.cfg`. The
`crt0` runtime uses `__RAM_START__` + `__RAM_SIZE__` to point cc65's
soft stack at the top of RAM.

## Limitations

- No stdio — `printf`, `putchar`, etc. aren't linked. Use the inline
  `chippy_putc` pattern shown in `hello.c` / `fizzbuzz.c` for output.
- cc65 V2.18 ships C89 only. Declare loop variables before the `for`
  block: `uint8_t i; for (i = 0; ...)`.
- No IRQ handling yet. If you wire an IRQ-driven peripheral, update
  the `IRQ` vector in `crt0.s` to point at your handler.
