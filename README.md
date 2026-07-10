# chippy

[![ci](https://github.com/nkane/chippy/actions/workflows/ci.yml/badge.svg)](https://github.com/nkane/chippy/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/nkane/chippy/branch/main/graph/badge.svg)](https://codecov.io/gh/nkane/chippy)
[![release](https://img.shields.io/github/v/release/nkane/chippy?sort=semver)](https://github.com/nkane/chippy/releases)
[![license](https://img.shields.io/github/license/nkane/chippy)](LICENSE)

A TUI 6502 emulator and debugger, written in Go.

![chippy onboarding screencast](docs/media/onboarding.gif)

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

- **TUI + DAP + WASM, one engine.** Same NMOS / 65C02 / 65C816 core powers the
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

### Homebrew (macOS)

```sh
brew tap nkane/tap
brew install --cask chippy
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

### nessy (NES emulator)

The NES emulator that was previously bundled here has moved to its
own repo: **[github.com/nkane/nessy](https://github.com/nkane/nessy)**.
nessy still imports the chippy 6502 core (`cpu`, `dap`, `peripheral`,
`expr`, `loader`, `symbols`, `trace`) as a Go module dependency; the
two projects version + release independently now.

See the nessy README for install, demos, DAP attach, save states,
and the headless recorder.

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
| `.hex`    | Intel HEX (record types `00` data, `01` EOF, `04` extended linear address → loads into 65816 banks >0) |
| `.o`      | ca65/cc65 object — linked via `ld65` (requires `-cfg`)        |

Flags:

| Flag     | Default | Meaning                                                |
|----------|---------|--------------------------------------------------------|
| `-rom`   | —       | Path to program (`.bin`, `.prg`, `.hex`, `.o`)         |
| `-addr`  | `32768` | Load address for raw `.bin` (ignored for other types)  |
| `-cfg`   | —       | ld65 linker config (required for `.o`)                 |
| `-dbg`   | auto    | cc65 `.dbg` symbol file; `<rom>.dbg` is tried by default |
| `-reset` | `0`     | Override reset vector. `0` keeps the existing `$FFFC/D` bytes (or falls back to the load address if those are zero) |
| `--cpu`  | `nmos`  | CPU variant: `nmos` (MOS 6502), `65c02` (WDC/Rockwell CMOS), `nes` (Ricoh 2A03 — NMOS minus decimal-mode arithmetic), or `65816` (WDC 65C816 — full 16-bit core, emulation + native, Tom Harte-validated; full 24-bit bank-aware address space — `:bank N` inspects banks >0) |
| `-trace` | —       | Write per-instruction execution trace to this file. Also toggleable at runtime via `:trace PATH \| :trace on \| :trace off`. |
| `-run-on-start` | `false` | Start the CPU running instead of paused. Pair with `-trace` for non-interactive capture (`chippy -rom prog.bin -trace t.log -run-on-start`). |
| `-dap`  | —       | Run as a [Debug Adapter Protocol](https://microsoft.github.io/debug-adapter-protocol/) server instead of the TUI. Accepts `stdio` (editor pipes stdin/stdout), `tcp:PORT` (server listens, editor connects out), `unix:PATH` (unix-domain socket, lowest-overhead local), or `inproc` (in-process loopback self-check). See [`docs/dap.md`](docs/dap.md) for the request list, transport benchmarks, and VS Code / nvim-dap onboarding. |
| `-dap-attach` | — | Connect out to a remote DAP server (`tcp:HOST:PORT`). Phase A: drives the `initialize` + `attach` handshake, prints capabilities + the first events, then disconnects. The TUI does not run yet — Phase B/C (CPUSource interface + DAP-backed source) wires the remote into the live TUI in a follow-up PR. Mutually exclusive with `-rom` and `-dap`. |
| `-text-buf-cap` | `65536` | TextOutput ($F001) buffer cap in bytes. Older bytes are evicted when full. `0` disables the bound. Dump the live buffer with `:textsave PATH`. |
| `-theme` | `default` | Color palette: `default` / `mono` / `protan` (red-green safe) / `tritan` (blue-yellow safe). `NO_COLOR=1` env forces `mono` regardless. Switch at runtime with `:theme NAME`; the choice persists across launches. |
| `-trace-replay` | — | Path to a prior `.trace` file. Opens the TUI in replay mode — `s` and `<` scroll through recorded frames instead of running the live CPU. The CPU register state is synced from the active frame so every panel renders as if paused at that PC. |
| `-diff` | — | With `-trace-replay`: load a second `.trace` and mark the first cycle where the two runs diverge. Press `d` for the side-by-side view, `D` to jump both cursors to the divergence. |

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
| `:bank $NN`                 | Select 65816 memory-panel bank `$00`–`$FF`   |
| `:da $XXXX` / `:da $BB:XXXX`| Pin disassembly to addr/symbol (65816 banks) |
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
| `:watch grid word x16`                   | Array watch: 16 LE words from `grid` |
| `:watch buf x8 sprite`                   | Array watch: 8 bytes, labelled       |
| `:watch player as {hp:byte, x:word, y:word}` | Struct overlay: named member rows |
| `:watch reg A`                           | Watch CPU register                  |
| `:watch reg A accumulator`               | Register watch with label           |
| `:rmwatch $0200` / `:rmwatch reg A`      | Remove a single watch               |
| `:clearwatch`                            | Remove all watches                  |

Aliases: `:w` for `:watch`, `:unwatch` for `:rmwatch`.

Array watches expand into indexed rows (`name[0]`, `name[1]`, …). The
element width is the watch's `byte`/`word` kind; `xN` (or `[N]`) sets the
count. When the `.dbg` carries a `size=` for the symbol, chippy seeds the
count automatically — but cc65 omits `size=` for most data globals, so
`xN` is usually required. Long arrays render the first few elements and
collapse the rest into a `… +N more` line.

**Struct overlays** (`:watch X as {field:width, …}`) expand into named member
rows read at `X + offset`. cc65's `.dbg` carries no struct member layout (all
`csym` types collapse to void), so the layout is user-declared. Each member is
`name:width` where width is `byte` or `word`; offsets auto-advance by width.
Override an offset with `name@N:width` (decimal or `$hex`) — handy for padded
or union layouts. Overlays persist in the watch state like any other watch.

### Trace replay

Available only when launched with `-trace-replay PATH` (the CPU stays paused;
`s` / `<` scroll recorded frames).

| Command            | Effect                                                            |
|--------------------|------------------------------------------------------------------|
| `:find EXPR`       | Jump to the next frame matching EXPR (e.g. `:find PC=$8042`, `:find A=$10 && X=0`) |
| `:rfind EXPR`      | Same, searching backward                                         |
| `:find` / `:rfind` | Repeat the last expression (sweep through matches)              |
| `:cycle N`         | Jump to the first frame at/after absolute cycle N (binary search, O(log N)) |
| `d`                | Toggle the side-by-side diff view (needs `-diff`)               |
| `D`                | Jump both cursors to the first divergence                       |

`:find` expressions use the same grammar as breakpoint conditions over the
frame's registers/flags (`A`, `X`, `Y`, `P`, `SP`, `PC`, `N V B D I Z C`). A
bare `=` is accepted as equality. Memory dereferences read live RAM, which is
stale during replay — stick to register/flag predicates.

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

Press `<` to rewind one instruction. Every step — explicit (`s`, `S`, `n`,
`f`) or free-run (`r`) — records a page-level copy-on-write delta beforehand,
kept in a 256-entry FIFO ring; the status bar shows `rwd:N` while non-empty.

For jumps deeper than that ring, **deep rewind** keeps periodic full-RAM
*keyframes* (one every 4096 steps) and reconstructs any earlier step by
restoring the nearest keyframe and replaying forward to the exact target:

| Command            | Effect                                                      |
|--------------------|-------------------------------------------------------------|
| `:rewind N`        | Step back N executed steps (keyframe replay for deep jumps) |
| `:rewind-budget MB`| Cap keyframe memory; sets the deep-rewind reach             |

Reach (steps) = `budget / 64 KiB × 4096`. At the default 128 MiB cap that's
~8.4M steps; `:rewind-budget 256` reaches ~16.7M. The budget is a ceiling —
a short run holds only the keyframes it produced — and the status bar shows
`deep:<reach>@<budget>` once keyframes exist. A deep rewind replays at most
4096 instructions (sub-millisecond on the cycle-accurate core). Replay assumes
deterministic execution between keyframes; live keyboard input is captured in
the snapshot, so buffered input replays correctly.

---

## Status

Implements all official NMOS 6502 opcodes, including packed-BCD `ADC`/`SBC`
when the `D` flag is set (NMOS semantics — the binary path drives N/V/Z and
the decimal path drives C and A).

Verified against [Klaus Dormann's 6502 functional test
suite](https://github.com/Klaus2m5/6502_65C02_functional_tests) (the
de-facto correctness gold standard for 6502 emulators) — passes the full
~30M-instruction sweep — plus Klaus's **interrupt test** (IRQ/NMI/BRK via a
feedback port) and Frank Kingswood's compact **AllSuiteA** smoke ROM. Run
locally with:

```sh
go test -tags=klaus -timeout 5m -run 'TestKlaus|TestAllSuiteA' ./cpu/...
```

The functional + AllSuiteA ROMs are downloaded on demand into the user
cache dir (set `CHIPPY_KLAUS_BIN` / `CHIPPY_ALLSUITE_BIN` to skip). The
interrupt test ships as as65 source only, so chippy vendors a ca65 port at
`cpu/testdata/6502_interrupt_test.ca65`; assemble it with cc65 and point
`CHIPPY_INTERRUPT_BIN` at the result (build steps in
`cpu/interrupt_rom_test.go`; CI does this automatically).

The CPU subset of **Wolfgang Lorenz's C64 test suite** (decimal, flag-edge,
and stable-illegal probes) is also run — the dumps are vendored at
`cpu/testdata/lorenz/` and each runs standalone against a tiny KERNAL-trap
harness, so a failure names the exact probe:

```sh
go test -tags=lorenz -timeout 5m -run TestLorenzSuite ./cpu/...
```

C64-hardware tests (CIA/SID/VIC, NMI/IRQ sourcing) are out of scope.

Per-opcode, chippy is fuzzed against **Tom Harte's ProcessorTests** — ~10,000
randomized initial→final cases for each 6502 opcode (registers, memory, cycle
count). The ~1 GB data set is not vendored; CI downloads + caches it, or point
`CHIPPY_HARTE_DIR` at a local `6502/v1` directory:

```sh
CHIPPY_HARTE_DIR=/path/to/6502/v1 go test -tags=harte -run TestHarte6502 ./cpu/...
# or omit the dir to download (pinned commit) into the user cache
```

Every stable opcode passes. JAM/KIL and the unstable illegals (SHA/SHX/SHY/TAS)
are skipped — their result is a magic constant the stable approximation doesn't
model. (65C02 is tracked separately.)

`TestHarte6502BusTrace` goes further and compares the **full per-cycle bus
trace** (address, data, read/write of every cycle including dummies) against
Tom Harte's `cycles` field — the highest-fidelity probe short of silicon.
228/238 opcodes match bus-exact; taken page-crossing branches and JSR/RTS
dummy-cycle ordering are skip-listed (they don't affect final state or cycle
count — no real ROM is impacted).

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

### TUI smoke tests (VHS)

End-to-end TUI behavior is recorded by [VHS](https://github.com/charmbracelet/vhs)
tapes under `test/smoke/`. Each `.tape` drives chippy through a real
TTY and renders a `.gif`; reviewers scrub the artifacts on PRs and CI
checks that the render didn't crash.

```sh
make smoke         # render every chippy tape
make smoke-clean   # remove rendered output
```

Requires `vhs`, `ttyd`, and `ffmpeg` on `$PATH`. Output lands in
`test/smoke/out/` (gitignored).

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
test/smoke/         VHS tape scripts + Makefile for TUI smoke tests
```

---

## Support

If chippy is useful to you, consider buying me a coffee:

<a href="https://buymeacoffee.com/nkane" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" height="60" width="217"></a>
