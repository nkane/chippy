# Debugging chippy programs from your editor

`chippy` ships with a [Debug Adapter Protocol](https://microsoft.github.io/debug-adapter-protocol/)
server so any DAP-aware editor can drive the emulator. The TUI is still
the default; DAP mode is a separate process you start with the `-dap`
flag.

## Launching the adapter

Two transports:

```sh
# stdio — editor spawns the binary and pipes stdin/stdout.
# Used by VS Code's default DebugAdapterExecutable.
chippy -dap stdio

# TCP — adapter listens, editor connects out.
# Used by nvim-dap's default `executable` block.
chippy -dap tcp:14785
```

`-dap` is mutually exclusive with the TUI: when set, chippy never opens
the alt-screen and instead speaks JSON-RPC over the chosen channel.

## VS Code

Copy `examples/dap/launch.json` into your project's `.vscode/launch.json`
and adjust paths. The launch config maps 1:1 to the CLI flags:

```jsonc
{
  "type": "chippy",
  "request": "launch",
  "name": "Run chippy",
  "rom": "${workspaceFolder}/build/program.bin",
  "cpuVariant": "65c02",
  "dbgPath": "${workspaceFolder}/build/program.dbg",
  "stopOnEntry": true
}
```

`stopOnEntry` defaults to true so the debugger pauses at the reset
vector. Press F5 again (or **Continue**) to start running.

## nvim-dap

Copy `examples/dap/nvim-dap.lua` into your config. It registers the
`chippy` adapter (TCP transport on 14785) and a default configuration
that launches a program from `cwd/build/program.bin`.

```lua
require("dap").adapters.chippy = {
  type = "server",
  port = 14785,
  executable = { command = "chippy", args = { "-dap", "tcp:14785" } },
}
```

`<F5>` to launch, `<F10>` step over, `<F11>` step into,
`<Shift-F11>` step out.

## Supported requests

| Request                          | Issue | Notes |
|----------------------------------|-------|-------|
| `initialize`                     | #47   | Negotiates capabilities; reports the full subset below in one shot. |
| `launch`                         | #47   | Takes `rom`, `loadAddr`, `resetVec`, `linkerCfg`, `dbgPath`, `cpuVariant`, `tracePath`, `stopOnEntry`. |
| `attach`                         | —     | Not supported. Returns an error. |
| `disconnect` / `terminate`       | #47   | Tears down the run goroutine, closes the trace file, exits. |
| `continue` / `pause`             | #50   | Continue spawns a CPU run goroutine; pause flips a signal. |
| `next` / `stepIn` / `stepOut`    | #50   | Step-over runs to PC+3 past a JSR; step-out runs until SP rises. |
| `threads`                        | #50   | One virtual thread (`id=1`, name=`cpu`). |
| `stackTrace`                     | #48   | Walks JSR frames via `cpu.DetectStackFrame`. |
| `scopes`                         | #48   | Two scopes per frame: Registers, Flags. |
| `variables`                      | #48   | ref=1 → A/X/Y/SP/PC/P/Cycles; ref=2 → 8 P-flag bits. |
| `setVariable`                    | #48   | Writes hex / decimal to a register or flag bit. |
| `setBreakpoints`                 | #49   | Source-line bps resolved through the `.dbg` source map. |
| `setInstructionBreakpoints`      | #49   | Address bps (`$XX`, `0xXX`, decimal). |
| `disassemble`                    | #51   | Variant-aware via `cpu.DisasmCPU`. |
| `readMemory` / `writeMemory`     | #51   | Bypasses MMIO — peripherals don't see debugger pokes. |
| `evaluate`                       | #52   | Watch / hover / debug-console expressions via `internal/expr`. |

## Known gaps

- `stepBack` is unsupported in DAP. The TUI's `<` rewind ring (#54)
  isn't yet wired to the adapter; reverse-step over DAP is filed as a
  follow-up.
- `attach` is unsupported — only `launch` boots a fresh debuggee.
- `disassemble` clamps negative `instructionOffset` to 0; backward
  walks would need the TUI's `walkBack` heuristic.
- Peripherals aren't snapshotted by reverse-step (see #62).
- The DAP server is a single session per process. To debug two ROMs
  at once, run two `chippy -dap` instances on different ports.

## Expression grammar (`evaluate`, conditional breakpoints)

Same compiler as the TUI:

```
A == $42
X > 10 && Y != 0
(A & $80) != 0
[$0042] == X
C && !Z
PC >= main
```

See `internal/expr/expr.go` for the full operator table.

## Troubleshooting

- **No output from a trace launched via DAP.** Same as the TUI: pass
  `"tracePath"` AND set `"stopOnEntry": false` (or send `continue`
  after launch). The CPU doesn't auto-run on launch.
- **`launch` returns `launch requires a 'rom' argument`.** Your config
  is missing the `rom` field. Path can be absolute or relative to the
  cwd of the chippy process.
- **Breakpoints come back `verified: false`.** The .dbg source map
  doesn't cover the requested (file, line). Confirm `dbgPath` resolves
  and that the file matches a `sym` entry's `file=` field in the .dbg.
