# chippy — WebAssembly playground

Browser shell that runs the same NMOS 6502 / 65C02 emulator chippy's
TUI and DAP server use, compiled to WebAssembly. Drop in a `.bin`,
`.prg`, or `.hex` (or pick a canned demo), and step through it with
registers + disassembly + memory + the Apple-1-style text peripheral
visible.

## Build + run locally

```sh
make           # builds chippy.wasm
make serve     # builds + serves on http://localhost:8080
```

The serve target uses Python's stdlib HTTP server; any static-file
server works. The page expects `chippy.wasm`, `wasm_exec.js`, and the
`demos/` directory to be siblings of `index.html`.

## Files

| file              | purpose                                                   |
|-------------------|-----------------------------------------------------------|
| `index.html`      | page shell — controls, register pane, panels              |
| `style.css`       | dark theme                                                |
| `chippy.js`       | JS driver: loads wasm, wires DOM ↔ `window.chippy` API    |
| `wasm_exec.js`    | Go runtime shim — copied verbatim from `$GOROOT/lib/wasm` |
| `chippy.wasm`     | the emulator, built from `cmd/chippy-wasm`                |
| `demos/*.bin`     | canned ROMs (copied from `example/`)                      |

## What the `chippy` global exposes

`cmd/chippy-wasm/main.go` installs `window.chippy` with these methods:

| call                       | behavior                                                  |
|----------------------------|-----------------------------------------------------------|
| `load(bytes, opts)`        | `{format, addr?, resetVec?}` — parses bytes, seeds reset vector when zero |
| `reset()`                  | resets the CPU (PC ← reset vector)                        |
| `step()`                   | one instruction                                           |
| `run(maxSteps)`            | budgeted free-run; returns `{steps, halted, pc}`          |
| `state()`                  | regs + flags + cycles + halted                            |
| `readMem(addr, len)`       | `Uint8Array`                                              |
| `writeMem(addr, byteOrU8)` | poke a byte or splat a slice                              |
| `disasm(addr, count)`      | `[{addr, bytes, text, size}]`                             |
| `textOutput()`             | the $F001 sink as a string                                |
| `clearTextOutput()`        | empty the sink                                            |
| `pushKey(byte)`            | queue a key for the $F004/$F005 PIA                       |
| `setVariant(name)`         | `"nmos"` or `"65c02"` — rebuilds the world                |

## What's missing vs. the desktop binary

By design — keeping the WASM blob small and the page self-contained:

- **No ld65 / `.o` pipeline.** The browser can't shell out to a native
  linker; bring a `.bin` or `.prg` instead.
- **No reverse-step / breakpoints / DAP server.** Step + run + read are
  enough for an "explore the demo" playground; the full debugger lives
  in the desktop CLI.
- **No source view.** The `.dbg` parser isn't wired in v1.
