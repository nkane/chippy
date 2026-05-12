# chippy

[![ci](https://github.com/nkane/chippy/actions/workflows/ci.yml/badge.svg)](https://github.com/nkane/chippy/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/nkane/chippy/branch/main/graph/badge.svg)](https://codecov.io/gh/nkane/chippy)
[![release](https://img.shields.io/github/v/release/nkane/chippy?sort=semver)](https://github.com/nkane/chippy/releases)
[![license](https://img.shields.io/github/license/nkane/chippy)](LICENSE)

A TUI 6502 emulator and debugger, written in Go.

`chippy` emulates the NMOS 6502 CPU and presents a terminal UI built with
[Bubble Tea](https://github.com/charmbracelet/bubbletea) for inspecting
registers, flags, the stack, live disassembly, and memory while you single-step
or free-run a program.

It speaks the ca65/cc65 toolchain natively: load a `.bin`, `.prg`, `.hex`, or
even an unlinked `.o` (chippy will run `ld65` for you), and any sibling
`.dbg` symbol file is auto-detected so the disassembly shows real names and
breakpoints can resolve to source lines.

---

## Why chippy

Plenty of 6502 emulators exist. chippy's pitch is **debugger-first**:

- **TUI + DAP + WASM, one engine.** Same NMOS / 65C02 core powers the
  terminal UI, the [Debug Adapter Protocol](docs/dap.md) server (so
  VS Code / nvim-dap / JetBrains can drive it), and the in-browser
  [WASM playground](https://nkane.dev/chippy/). One implementation, three
  surfaces.
- **Source-level debugging from C and ca65.** Auto-detected `.dbg` files
  turn `.bin` addresses into `file:line` and symbol names — breakpoints,
  watches, and conditional expressions resolve against the same names you
  wrote.
- **Reverse-step that actually scales.** Page-level copy-on-write
  snapshots cost hundreds of bytes per step instead of 64 KiB, so the
  rewind ring works during free-run too (a 1000-iter tight loop fits
  in <1 MiB).
- **MMIO peripherals you can poke from BASIC-era ROMs.** Apple-1 style
  TextOutput at $F001 and KeyboardInput at $F004/$F005 ship out of the
  box; the same `peripheral` package will host VIA 6522 next.
- **Real 6502 + 65C02 compliance.** Klaus Dormann's functional tests pass
  end-to-end for both variants; an exhaustive BCD sweep covers every
  ADC/SBC input combination.

Compared to:

- **[py65](https://github.com/mnaberez/py65)** — Python, no source-level
  debug, no DAP. Good for scripting.
- **[lib6502](http://www.piumarta.com/software/lib6502/)** — C library
  to embed in a host; no debugger of its own.
- **[visual6502](http://www.visual6502.org/)** — gate-level transistor
  simulation. Slower and not interactive; chippy is for development
  workflow, visual6502 is for hardware archaeology.

---

## Install

### Homebrew (macOS / Linux)

```sh
brew tap nkane/tap
brew install chippy
```

### Debian / Ubuntu

```sh
curl -LO https://github.com/nkane/chippy/releases/latest/download/chippy_<VERSION>_linux_amd64.deb
sudo dpkg -i chippy_<VERSION>_linux_amd64.deb
```

### Fedora / RHEL

```sh
sudo rpm -i https://github.com/nkane/chippy/releases/latest/download/chippy_<VERSION>_linux_amd64.rpm
```

### Alpine

```sh
sudo apk add --allow-untrusted chippy_<VERSION>_linux_amd64.apk
```

### Arch Linux (AUR)

```sh
yay -S chippy-bin
# or any other AUR helper
```

### Prebuilt tarballs

Grab a release archive for your platform from the
[releases page](https://github.com/nkane/chippy/releases) and drop the
`chippy` binary on your `$PATH`. Builds available for darwin/linux on
amd64+arm64 and windows on amd64. `cosign verify-blob` recipe in
[`SECURITY.md`](SECURITY.md) verifies the bundled signature.

### From source

```sh
go install github.com/nkane/chippy/cmd/chippy@latest
```

Or build from a checkout:

```sh
git clone git@github.com:nkane/chippy.git
cd chippy
go build ./cmd/chippy
go test ./...
```

Optional: install [cc65](https://cc65.github.io/) to assemble your own
programs.

---

## Quick start

```sh
# Built-in dummy program (no flags)
./chippy

# Load a raw binary at $8000 (the default load address)
./chippy -rom program.bin

# Load and let chippy invoke ld65 for you
./chippy -rom program.o -cfg linker.cfg

# Try the bundled examples (see example/README.md for descriptions)
cd example
make                              # builds every demo .bin + .dbg
../chippy -rom load_five.bin
../chippy -rom fibonacci.bin      # or count_to_ten / stack_demo / bcd_add
```

Once it's running, press `?` for an in-app help modal, or `:` to open the
command line.

---

## Loading programs

`chippy` auto-detects format by extension:

| Extension | Format                                                        |
|-----------|---------------------------------------------------------------|
| `.bin`    | Raw bytes — placed at `-addr` (default `$8000`)               |
| `.prg`    | Commodore-style: first 2 bytes = LE load address              |
| `.hex`    | Intel HEX (record types `00` data, `01` EOF)                  |
| `.o`      | ca65/cc65 object — linked via `ld65` (requires `-cfg`)        |

Flags:

| Flag     | Default | Meaning                                                |
|----------|---------|--------------------------------------------------------|
| `-rom`   | —       | Path to program (`.bin`, `.prg`, `.hex`, `.o`)         |
| `-addr`  | `32768` | Load address for raw `.bin` (ignored for other types)  |
| `-cfg`   | —       | ld65 linker config (required for `.o`)                 |
| `-dbg`   | auto    | cc65 `.dbg` symbol file; `<rom>.dbg` is tried by default |
| `-reset` | `0`     | Override reset vector. `0` keeps the existing `$FFFC/D` bytes (or falls back to the load address if those are zero) |
| `--cpu`  | `nmos`  | CPU variant: `nmos` (MOS 6502) or `65c02` (WDC/Rockwell CMOS) |
| `-trace` | —       | Write per-instruction execution trace to this file. Also toggleable at runtime via `:trace PATH \| :trace on \| :trace off`. |
| `-run-on-start` | `false` | Start the CPU running instead of paused. Pair with `-trace` for non-interactive capture (`chippy -rom prog.bin -trace t.log -run-on-start`). |
| `-dap`  | —       | Run as a [Debug Adapter Protocol](https://microsoft.github.io/debug-adapter-protocol/) server instead of the TUI. Accepts `stdio` (editor pipes stdin/stdout) or `tcp:PORT` (server listens, editor connects out). See [`docs/dap.md`](docs/dap.md) for the request list, supported launch arguments, and VS Code / nvim-dap onboarding. |
| `-text-buf-cap` | `65536` | TextOutput ($F001) buffer cap in bytes. Older bytes are evicted when full. `0` disables the bound. Dump the live buffer with `:textsave PATH`. |
| `-theme` | `default` | Color palette: `default` / `mono` / `protan` (red-green safe) / `tritan` (blue-yellow safe). `NO_COLOR=1` env forces `mono` regardless. Switch at runtime with `:theme NAME`; the choice persists across launches. |

Examples:

```sh
./chippy -rom program.bin -addr 0x8000 -reset 0x8000
./chippy -rom program.prg                   # load addr from header
./chippy -rom program.hex                   # load addr from records
./chippy -rom program.o -cfg nes.cfg        # ld65 invoked for you
./chippy -rom program.bin -dbg /tmp/x.dbg   # explicit debug file
```

---

## ca65 / cc65 workflow

Recommended path — assemble + link yourself, then load the `.bin`. The
sibling `.dbg` is picked up automatically:

```sh
ca65 -g prog.s -o prog.o
ld65 -C linker.cfg -o prog.bin --dbgfile prog.dbg prog.o
./chippy -rom prog.bin
```

Or hand chippy the `.o` and let it run `ld65`:

```sh
./chippy -rom prog.o -cfg linker.cfg
```

In this mode the `.dbg` is generated in a temp directory and loaded
automatically.

When symbols are loaded:

- Disassembly shows names instead of raw addresses (`JSR init` rather than
  `JSR $8042`)
- Labels are printed inline above their target instruction
- `:bp main`, `:goto main`, `:watch score` etc. accept symbol names
- Source-line breakpoints (`:bp main.s:42`) work — see below
- Source view (`v`) shows your `.s` file with the current line highlighted

---

## Keybinds

### Execution

| Key   | Action                                            |
|-------|---------------------------------------------------|
| `s`   | Single-step one instruction                       |
| `S`   | Step 16 instructions (stops on bp / mem watch)    |
| `n`   | Step over (skips JSR by setting one-shot bp at return) |
| `f`   | Run to next source line (uses `.dbg` info)        |
| `r`   | Toggle run / pause                                |
| `R`   | Hard reset CPU                                    |
| `+` `=` | Increase target speed                           |
| `-` `_` | Decrease target speed                           |
| `0`   | Speed: max (no throttle)                          |

### Breakpoints

| Key   | Action                                            |
|-------|---------------------------------------------------|
| `b`   | Toggle plain breakpoint at current `PC`           |
| `B`   | Open breakpoint manager modal                     |

In the BP manager modal: `j/k` move cursor, `e` toggle enable, `d` (or `x`,
`Delete`) delete, `enter` jump PC to bp, `esc/q/B` close.

### Views

| Key       | Action                                       |
|-----------|----------------------------------------------|
| `v`       | Toggle source view ↔ disassembly view        |
| `[` `]`   | Scroll disasm by 1                           |
| `{` `}`   | Scroll disasm by 8                           |
| `'`       | Re-anchor disasm to follow PC                |
| `j` `k`   | Scroll memory view by `$10` (also `↓`/`↑`)   |
| `J` `K`   | Scroll memory view by `$100` (also `PgDn`/`PgUp`) |
| `g` `G`   | Memory view to `$0000` / `$FF00`             |

### Other

| Key   | Action                                            |
|-------|---------------------------------------------------|
| `:`   | Open command line                                 |
| `?`   | Help modal (`q` to dismiss)                       |
| `q`   | Quit (saves state)                                |

---

## Commands

Type `:` to open the command line, then any of:

### Navigation

| Command                     | Effect                                       |
|-----------------------------|----------------------------------------------|
| `:goto $XXXX` / `:g $XXXX`  | Jump memory view to address (or symbol)      |
| `:pc $XXXX`                 | Force `PC` to address                        |
| `:run $XXXX`                | Set one-shot bp at address and start running |
| `:speed N`                  | Throttle to N Hz (`0` = unthrottled)         |

### Breakpoints

`:bp` accepts an address, symbol, or `file.s:line` source location, plus
optional modifiers:

| Form                                | Effect                                  |
|-------------------------------------|-----------------------------------------|
| `:bp $8042`                         | Toggle plain bp at address              |
| `:bp main`                          | Toggle bp at symbol `main`              |
| `:bp main.s:14`                     | Toggle bp at source line (needs `.dbg`) |
| `:bp $8042 once`                    | One-shot bp (deletes itself on hit)     |
| `:bp loop hits 5`                   | Break on the 5th hit                    |
| `:bp main if A==$FF`                | Conditional (🔶) — see expression syntax |
| `:bp $8000 log A={A} PC={PC}`       | Log point (📜) — prints, never pauses   |

Modifiers can be combined: `:bp main.s:42 if A==$FF hits 3 log A={A} X={X}`.

If a `file.s:line` reference can't be resolved (missing `.dbg`, file not in
debug info, no instruction emitted on that line), the bp is created in the
**rejected** state (💩) so you see it in the BP manager rather than just a
status flash.

#### Sigils (gutter glyphs)

| Sigil | Meaning                                |
|-------|----------------------------------------|
| 🛑    | Plain breakpoint                       |
| 🔶    | Conditional breakpoint                 |
| 📜    | Log point (prints, never pauses)       |
| 💩    | Rejected (unresolved source line)      |
| 👉    | Current `PC`                           |

### Memory watchpoints

Trigger when the CPU reads or writes a tracked address. Same modifier
syntax as `:bp` (`once`, `hits N`, `if EXPR`, `log MSG`).

| Command                                      | Effect                              |
|----------------------------------------------|-------------------------------------|
| `:bpr $0200`                                 | Break on any **read** of $0200      |
| `:bpw ram_flag`                              | Break on **write** to symbol        |
| `:bprw $FFFC`                                | Break on **either** read or write   |
| `:bpw $0200 if A==$FF`                       | Conditional                         |
| `:bpw score log score={[$0200]} PC={PC}`     | Log point — never pauses            |
| `:rmbpr $0200` / `:rmbpw …` / `:rmbprw …`    | Remove                              |

Watched bytes are colored in the memory hex view:

| Color   | Meaning           |
|---------|-------------------|
| Blue    | 👁 read watch     |
| Red     | ✏ write watch     |
| Magenta | 🔁 read + write   |

### Watches

The watch panel shows live values of registers and memory cells.

| Command                                  | Effect                              |
|------------------------------------------|-------------------------------------|
| `:watch $0200`                           | Watch byte at $0200                 |
| `:watch $0200 word`                      | Watch 16-bit LE word                |
| `:watch score`                           | Watch by symbol                     |
| `:watch $0200 byte player x`             | Watch with custom label             |
| `:watch reg A`                           | Watch CPU register                  |
| `:watch reg A accumulator`               | Register watch with label           |
| `:rmwatch $0200` / `:rmwatch reg A`      | Remove a single watch               |
| `:clearwatch`                            | Remove all watches                  |

Aliases: `:w` for `:watch`, `:unwatch` for `:rmwatch`.

### Misc

| Command         | Effect                                |
|-----------------|---------------------------------------|
| `:help` / `:?`  | Open help modal                       |
| `:q` / `:quit`  | (Hint to use `q` outside the prompt)  |

---

## Expression syntax (conditions and log templates)

Used by `:bp ... if EXPR` and `:bpw ... if EXPR` for conditions, and inside
`{...}` substitutions in `log MSG` templates.

### Operands

- **Registers:** `A`, `X`, `Y`, `P`, `SP` (`S` is an alias), `PC`
- **Flags:** `N`, `V`, `B`, `D`, `I`, `Z`, `C` — evaluate to `0` or `1`
- **Numeric literals:** `$FF` / `0xFF` / `255` / `0b1010`
- **Symbols:** any name in the loaded `.dbg` (resolves to its address)
- **Memory deref:** `[$XXXX]` — the byte at that address (works with symbols
  too: `[score]`)

### Operators

`==` `!=` `<` `<=` `>` `>=` `&&` `||` `!` `+` `-` `*` `/` `%` `&` `|` `^` `<<` `>>` `( )`

### Examples

```
A == $FF
X > 0 && Y < 10
[score] >= $64
P & $80 != 0
(A + X) == $42
```

### Log point template

`{EXPR}` substitutes the value of EXPR (formatted as `$XX` for bytes, `$XXXX`
for words). Anything else is literal text.

```
:bp main log entered main, A={A} X={X} PC={PC}
:bpw score log score={[score]} cycle={cycles}
```

`{cycles}` is recognized as a special token that prints the CPU cycle count.

---

## Persistence

Per-ROM state is saved to `~/.chippy/state-<basename>.json` and reloaded on
next launch. Persisted:

- Plain + rich breakpoints (with conditions, hit limits, log messages,
  source tags)
- Memory watchpoints
- Memory view position
- Watch panel entries
- Throttle speed
- Disasm scroll anchor

Conditions are recompiled at load time; if a previously-good condition
becomes invalid (e.g. you removed the symbol it referenced), the bp loads
with `condFn` nil and effectively becomes a plain bp.

---

## Layout

```
┌ Registers ──┐ ┌ Disassembly ────────────────┐
│ A:00 X:00 Y │ │ > $8000  LDA #$00           │
│ SP:FD PC:80 │ │   $8002  TAX                │
└─────────────┘ │   ...                       │
┌ Flags ──────┐ └─────────────────────────────┘
│ n v U b d I │ ┌ Memory ─────────────────────┐
└─────────────┘ │ $0000: 00 00 ... ........   │
┌ Stack ──────┐ └─────────────────────────────┘
│ $01FE: 00   │ ┌ Watches ────────────────────┐
└─────────────┘ │ score  $0200  $00           │
                └─────────────────────────────┘
status: ready
```

The disassembly panel is replaced by a source view when you press `v` (if
a `.dbg` is loaded). A breakpoint manager modal opens with `B`; the help
modal opens with `?`.

The stack panel detects JSR-pushed return-address pairs and renders them as
`ret $XXXX  callee  file:NN` rows, collapsing adjacent non-frame bytes into
single `(N bytes)` lines so the call chain stays readable. Press `T` to
toggle back to the raw one-byte-per-row layout.

The memory panel has a byte-level cursor (arrow keys move ±1 / ±$10, view
auto-scrolls). Press `e` at the cursor to poke a byte: type 1–2 hex digits,
Enter commits, Esc cancels. Edits are runtime-only — `R` (reset) reloads
the original program bytes.

The command prompt (`:`) remembers up to 100 entries in `~/.chippy/history`.
Up / Down walk recent commands; Tab completes the verb (when there's no
space yet) or the symbol after `:bp` / `:goto` / `:watch` etc.; Ctrl-R
opens a reverse-incremental search through history — each keystroke
narrows the match, Ctrl-R again walks to the next older one, Esc restores
the original line, Enter accepts.

Press `<` to rewind one instruction. Each explicit step (`s`, `S`, `n`,
`f`) records a full CPU + RAM snapshot beforehand, kept in a 256-entry
FIFO ring; the status bar shows `rwd:N` while non-empty. Free-run via
`r` does NOT snapshot — the 64 KiB-per-step cost would dominate at multi-MHz
throughput — so reverse-step covers single-stepping sessions, not whole
program executions.

---

## Status

Implements all official NMOS 6502 opcodes, including packed-BCD `ADC`/`SBC`
when the `D` flag is set (NMOS semantics — the binary path drives N/V/Z and
the decimal path drives C and A).

Verified against [Klaus Dormann's 6502 functional test
suite](https://github.com/Klaus2m5/6502_65C02_functional_tests) (the
de-facto correctness gold standard for 6502 emulators) — passes the full
~30M-instruction sweep. Run locally with:

```sh
go test -tags=klaus -timeout 5m -run TestKlaus ./internal/cpu/...
```

The ROM is GPL-3.0 so it is downloaded on demand into the user cache dir
on first run rather than vendored. Set `CHIPPY_KLAUS_BIN=/path/to/rom` to
use a local copy.

Stable undocumented ("illegal") opcodes are also implemented:
`LAX`, `SAX`, `DCP`, `ISC`, `SLO`, `RLA`, `SRE`, `RRA`, `ANC`, `ALR`, `ARR`,
`SBX`, the `SBC` alias at `$EB`, and the family of multi-byte/multi-cycle
NOPs that real silicon decodes at undocumented slots. `RRA` and `ISC` honor
decimal mode. The unstable opcodes (AHX/SHA, SHY, SHX, TAS, LAS, XAA, KIL/JAM)
remain decoded as 1-byte NOPs since their behavior depends on bus capacitance
or halts the CPU.

---

## Tests

```sh
go test ./...
```

The TUI package has headless tests for the memory watchpoint data plane
(`internal/tui/membp_test.go`) that exercise `WBus` + `processMemHits`
without the bubble-tea runtime.

---

## Project layout

```
cmd/chippy/         CLI entrypoint, flag parsing, loader/wiring
internal/cpu/       6502 core: CPU, RAM, opcodes, disassembler
internal/loader/    .bin/.prg/.hex/.o loaders
internal/symbols/   cc65 .dbg parser, symbol + source-line tables
internal/tui/       Bubble Tea model, panels, modals, commands
example/            Bundled ca65 demo programs (source + shared linker cfg + Makefile)
example/c/          Same idea, but the source is C — cc65 → ca65 → ld65 pipeline
```

---

## Support

If chippy is useful to you, consider buying me a coffee:

<a href="https://buymeacoffee.com/nkane" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" height="60" width="217"></a>
