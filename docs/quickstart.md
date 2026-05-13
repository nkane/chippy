# Quick start

Five minutes from `brew install` to single-stepping your first ROM.

## 1. Install

=== "macOS / Linux (Homebrew)"

    ```sh
    brew tap nkane/tap
    brew install chippy
    ```

=== "Debian / Ubuntu"

    ```sh
    curl -LO https://github.com/nkane/chippy/releases/latest/download/chippy_<VERSION>_linux_amd64.deb
    sudo dpkg -i chippy_*.deb
    ```

=== "Arch Linux"

    ```sh
    yay -S chippy-bin
    ```

=== "From source"

    ```sh
    go install github.com/nkane/chippy/cmd/chippy@latest
    ```

Verify:

```sh
chippy --help
```

## 2. Try a canned ROM

The repo ships small ca65 demos under `example/`. Clone, build with
`cc65`, run:

```sh
git clone git@github.com:nkane/chippy.git
cd chippy/example
make
chippy -rom count_to_ten.bin
```

You should see the TUI: register pane, disassembly, stack, memory,
text-output, status bar. The CPU starts paused at the reset vector.

## 3. Step + observe

| key       | action                                        |
|-----------|-----------------------------------------------|
| `s`       | single step (one instruction)                 |
| `n`       | step over JSR (run to RTS)                    |
| `r`       | run / pause                                   |
| `<`       | reverse step                                  |
| `b`       | toggle breakpoint at PC                       |
| `:`       | command line (see help modal)                 |
| `?` / `h` | help modal                                    |
| `q`       | quit                                          |

Try:

1. Press `s` a few times — watch `X` increment from `$00` to `$01`,
   `$02`, …, then stop at `$0A`.
2. Press `<` once to reverse-step.
3. Press `r` to free-run; press `r` again to pause.

## 4. Load your own program

ca65/cc65 emit `.bin` (raw image), `.prg` (with leading load address),
`.hex` (Intel HEX), or `.o` (objects ld65 must link). chippy auto-detects
by extension:

```sh
chippy -rom myprog.bin -addr 0x8000          # raw bin
chippy -rom myprog.prg                       # .prg
chippy -rom myprog.o -cfg myprog.cfg         # .o + linker config
```

A sibling `.dbg` symbol file (the `--dbgfile` output from `ld65 -g`)
gets picked up automatically — disassembly shows real symbol names,
breakpoints can target `:bp main` or `:bp src.s:42`.

## 5. Editor integration (DAP)

For a step-debugger experience in VS Code / nvim / JetBrains, see
[DAP server reference](dap.md). The short version:

- VS Code: install [the chippy extension](https://github.com/nkane/chippy/tree/main/extension/vscode-chippy)
  and use `examples/dap/launch.json` as a template.
- nvim-dap: copy `examples/dap/nvim-dap.lua` into your config.

Other editors that speak DAP work too — see the [editor matrix](editors.md).

## Next steps

- Browse the [example programs](https://github.com/nkane/chippy/tree/main/example)
  organised by category (math, I/O, interrupts, CMOS-only).
- Read the [DAP reference](dap.md) for the full editor-side feature
  set (reverse-step, conditional breakpoints, function breakpoints,
  exception breakpoints, evaluate expressions).
- Try the [browser playground](/chippy/playground/) — same
  emulator, no install.
