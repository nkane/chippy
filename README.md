# chippy

A TUI 6502 emulator with built-in debugger, written in Go.

`chippy` emulates the NMOS 6502 CPU and presents a terminal UI built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea) for inspecting
registers, flags, the stack, live disassembly, and memory while you single-step
or free-run a program.

## Install

```sh
go install github.com/nkane/chippy/cmd/chippy@latest
```

Or build from this repo:

```sh
go build ./cmd/chippy
```

## Usage

Run the built-in demo program:

```sh
./chippy
```

### Loading programs

`chippy` auto-detects format by file extension:

| Extension | Format                                                |
|-----------|-------------------------------------------------------|
| `.bin`    | Raw bytes — placed at `-addr` (default `$8000`)       |
| `.prg`    | Commodore-style: first 2 bytes = LE load address      |
| `.hex`    | Intel HEX (record types `00` data, `01` EOF)          |
| `.o`      | ca65/cc65 object — linked via `ld65` (requires `-cfg`)|

```sh
./chippy -rom program.bin -addr 0x8000 -reset 0x8000
./chippy -rom program.prg                  # load addr from header
./chippy -rom program.hex                  # load addr from records
./chippy -rom program.o -cfg nes.cfg       # ld65 invoked for you
```

If `-reset` is omitted, chippy uses the bytes already at `$FFFC/$FFFD`,
falling back to the file's load address if those bytes are zero.

### ca65 / cc65 workflow

The recommended path is to assemble + link yourself, then load the `.bin`.
chippy will auto-load a sibling `.dbg` for symbols:

```sh
ca65 -g prog.s -o prog.o
ld65 -C linker.cfg -o prog.bin --dbgfile prog.dbg prog.o
./chippy -rom prog.bin                     # picks up prog.dbg automatically
```

You can also point chippy directly at the `.o` and let it run `ld65`:

```sh
./chippy -rom prog.o -cfg linker.cfg
```

In this mode the `.dbg` is generated in a temp directory and loaded
automatically. Pass `-dbg path/to/file.dbg` to override symbol-file detection.

When symbols are loaded, the disassembly shows names instead of raw addresses
(`JSR init` rather than `JSR $8042`) and labels are printed inline above their
target instruction.

## Keybinds

| Key       | Action                                |
|-----------|---------------------------------------|
| `s`       | Single-step one instruction           |
| `r`       | Toggle run / pause                    |
| `R`       | Hard reset CPU                        |
| `b`       | Toggle breakpoint at current `PC`     |
| `j` / `k` | Scroll memory view by `$10`           |
| `J` / `K` | Scroll memory view by `$100`          |
| `q`       | Quit                                  |

## Layout

```
┌ Registers ──┐ ┌ Disassembly ─────────────┐
│ A:00 X:00 Y │ │ > $8000  LDA #$00         │
│ SP:FD PC:80 │ │   $8002  TAX              │
└─────────────┘ │   ...                     │
┌ Flags ──────┐ └───────────────────────────┘
│ n v U b d I │ ┌ Memory ───────────────────┐
└─────────────┘ │ $0000: 00 00 ... ........ │
┌ Stack ──────┐ └───────────────────────────┘
│ $01FE: 00   │
└─────────────┘
```

## Status

Implements all official NMOS 6502 opcodes. Decimal mode arithmetic adjustments
are not yet applied (flag is tracked but `ADC`/`SBC` operate in binary mode).
Unofficial / illegal opcodes currently behave as 1-byte NOPs.

## Tests

```sh
go test ./...
```
