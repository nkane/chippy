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

| file              | purpose                                                                          |
|-------------------|----------------------------------------------------------------------------------|
| `index.html`      | page shell — controls, register pane, panels. CSP `default-src 'self'` enforced. |
| `style.css`       | dark theme; reflows to a single column under 800 px                              |
| `chippy.js`       | JS driver: loads wasm, wires DOM ↔ `window.chippy` API, share-link permalink     |
| `sw.js`           | service worker — cache-first for static assets, offline-ready on repeat visits   |
| `wasm_exec.js`    | Go runtime shim — copied verbatim from `$GOROOT/lib/wasm`                        |
| `chippy.wasm`     | the emulator, built from `cmd/chippy-wasm`                                       |
| `demos/*.bin`     | canned ROMs (copied from `example/`)                                             |

## Share-link permalink

The **share** button copies a URL with the loaded ROM encoded into the
fragment (`#rom=<base64>&format=bin&addr=0x8000&variant=nmos`). Opening
the URL on another machine re-loads the ROM into the playground without
the user needing to drag the file in again. Bytes stay client-side —
the fragment never hits the server.

## Boot-error banner

If `chippy.wasm` fails to load (offline, blocked, wrong MIME), a red
banner above the panes shows the underlying error and a hint to use
`make -C web serve` instead of opening `index.html` via `file://`.

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
