# Debugging NES ROMs

nessy ships chippy's DAP server alongside the Ebiten game loop. Any DAP-speaking editor can attach — chippy's TUI is the easiest path, but VS Code / nvim-DAP / Zed work too.

## One-shell debug launch

The fastest way:

```sh
chippy -nessy roms/demos/hello-bg/hello-bg.nes
```

That spawns nessy in the background, dials its DAP listener, and opens the TUI in attach mode paused at the reset vector. No second terminal needed.

## Two-shell attach

If you want to keep nessy and chippy in separate windows:

**Terminal 1:**

```sh
nessy -wait-for-debugger game.nes
```

`-wait-for-debugger` pauses the CPU at boot until a DAP client attaches.

**Terminal 2:**

```sh
chippy -dap-attach tcp:localhost:14785
```

The TUI's panels (registers / disasm / memory / stack / source) all show live state synced from nessy.

## Symbol files

If your ROM was built with `ca65 -g` + `ld65 --dbgfile`, nessy auto-detects the `.dbg` sibling and feeds it to chippy through the DAP `Source` event so the TUI's source panel highlights the current line in real `.s` code.

```sh
nessy game.nes              # nessy auto-detects game.dbg
nessy -dbg path/to/game.dbg # explicit override
```

## What the TUI gives you

- **Step** (`s`), **step-into** / **step-over** (`o` / `O`), **step-back** (`b`), **continue** (`r`).
- **Breakpoints** — toggle at any source line or instruction address; survive across reattach.
- **Watch expressions** — read / write watches on any RAM address; the expression engine handles arithmetic + label resolution.
- **Memory editor** — byte-level cursor, hex edit mode, `:goto` jumps anywhere.
- **Stack panel** with JSR-frame annotation (`ret $XXXX  callee  file:NN`).
- **Source panel** with the current PC highlight + clickable line numbers.
- **Quake-style console** (\`backtick\`) for scrollback over `:` commands.

See [`dap.md`](../dap.md) for protocol details and [`editors.md`](../editors.md) for the per-editor support matrix.

## Live demo

![nessy-attach](https://github.com/nkane/chippy/raw/main/test/smoke/out/nessy-attach.gif)

Recorded via [VHS](https://github.com/charmbracelet/vhs). Tape source: [`test/smoke/nessy-attach.tape`](https://github.com/nkane/chippy/blob/main/test/smoke/nessy-attach.tape).
