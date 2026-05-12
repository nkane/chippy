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

`stopOnEntry` is honored:

- absent (default): pause at the reset vector and emit a stopped(entry) event
- `true`: same as absent
- `false`: skip the entry pause and auto-start the run loop after launch

Same field on `attach` skips/emits the entry stopped event without
spawning a new run goroutine — the host process drives execution.

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
| `attach`                         | #87   | Supported when the host process pre-populates the debuggee via `Server.AttachExisting`. The in-TUI listener that ties this to the `:dap` command is filed as a follow-up (#97). |
| `disconnect` / `terminate`       | #47   | Tears down the run goroutine, closes the trace file, exits. |
| `continue` / `pause`             | #50   | Continue spawns a CPU run goroutine; pause flips a signal. |
| `next` / `stepIn` / `stepOut`    | #50   | Step-over runs to PC+3 past a JSR; step-out runs until SP rises. |
| `stepBack`                       | #79   | Pops one snapshot from the 256-entry rewind ring and restores CPU+RAM. Only pre-step paths push — `continue` runs aren't reversible. |
| `threads`                        | #50   | One virtual thread (`id=1`, name=`cpu`). |
| `stackTrace`                     | #48   | Walks JSR frames via `cpu.DetectStackFrame`. |
| `scopes`                         | #48   | Two scopes per frame: Registers, Flags. |
| `variables`                      | #48   | ref=1 → A/X/Y/SP/PC/P/Cycles; ref=2 → 8 P-flag bits. |
| `setVariable`                    | #48   | Writes hex / decimal to a register or flag bit. |
| `setBreakpoints`                 | #49, #81 | Source-line bps resolved through the `.dbg` source map. Honors `condition` / `hitCondition` (integer) / `logMessage` (with `{expr}` interpolation). |
| `setInstructionBreakpoints`      | #49, #81 | Address bps (`$XX`, `0xXX`, decimal). Same modifier support as source bps. |
| `setFunctionBreakpoints`         | #82, #81 | Symbol-name bps via `syms.LookupName`. Same modifier support. |
| `loadedSources`                  | #84   | Lists every file the loaded `.dbg` references. |
| `source`                         | #84   | Returns file contents for a previously-listed Source. Basename-matches if the client passes an absolute path. |
| `disassemble`                    | #51, #80 | Variant-aware via `cpu.DisasmCPU`. Negative `instructionOffset` resolves via `cpu.WalkBack` for pre-context. |
| `readMemory` / `writeMemory`     | #51   | Bypasses MMIO — peripherals don't see debugger pokes. |
| `evaluate`                       | #52   | Watch / hover / debug-console expressions via `internal/expr`. |
| `completions`                    | #85   | Debug-console autocomplete: registers, flag bits, and loaded `.dbg` symbols. |
| `setExceptionBreakpoints`        | #83   | Toggles "pause on BRK" via the `brk` filter. Run loop checks for `$00` opcode before each `cpu.Step()`. |
| `exceptionInfo`                  | #83   | Describes the last-fired exception (`brk` with PC). |

## Known gaps

- `attach` requires the host process to wire a debuggee via `Server.AttachExisting`. The cross-process / in-TUI flow is filed at #97.
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
