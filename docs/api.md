# Public API

chippy's 6502 core ships as an importable Go library. The packages
below live at the module root (`github.com/nkane/chippy/<pkg>`) so
external modules — including the standalone [nessy](nessy/index.md)
emulator — can build on them. Everything under `internal/` is
private to this module and exempt from the compatibility promise.

```go
import "github.com/nkane/chippy/cpu"
```

## Stability

The bare `vX.Y.Z` chippy tags are the library's semver. The
**contract** types below are the load-bearing surface — changes to
them are breaking and bump the major version. Convenience helpers
may gain additions in minor releases; signatures stay stable within
a major.

| Package | Role | Stability |
|---|---|---|
| [`cpu`](#cpu) | 6502 / 65C02 / 2A03 core + bus | contract |
| [`peripheral`](#peripheral) | MMIO devices | contract |
| [`symbols`](#symbols) | cc65 `.dbg` parser | stable |
| [`loader`](#loader) | program loaders | stable |
| [`expr`](#expr) | watch/condition expressions | stable |
| [`trace`](#trace) | execution-trace replay | stable |
| [`dap`](#dap) | Debug Adapter Protocol server | stable |

## cpu

The processor core. Variant-dispatched opcode tables cover NMOS
6502, WDC 65C02, and the Ricoh 2A03 (NES — NMOS minus BCD).

**Contract types** — the integration surface every host + peripheral
builds against:

- `Bus` — `Read(addr uint16) byte` / `Write(addr uint16, v byte)`. The
  CPU's whole view of memory. Implement it to back the CPU with any
  storage / routing.
- `Peripheral` — a `Bus`-attached device claiming an address range
  (`Range() (lo, hi uint16)` + `Read` / `Write`). Registered on `MMIO`.
- `Ticker` — `Tick(cycles int)`; optional on a `Bus` / `Peripheral`.
  `Step()` calls it with each instruction's cycle delta so time-based
  devices (PPU, APU, timers) advance. The basis for nessy's PPU/APU.
- `Variant` — `VariantNMOS` | `VariantCMOS65C02` | `VariantNES`; picks
  the opcode table.

**Core types**:

- `CPU` — registers (`A,X,Y,SP,P,PC,Cycles`), `Step() int`, `Reset()`,
  interrupt lines (`AssertIRQSource`/`ClearIRQSource`/`TriggerNMI`),
  `Stall(int)` + `CurrentCycle()` for bus-stealing DMA, `FullState`
  snapshot/restore (`SaveFullState`/`LoadFullState`).
- `MMIO` — a `Bus` that routes ranges to registered `Peripheral`s over
  an inner fallback `Bus`; fans `Tick` out to peripherals.
- `RAM` — flat 64 KiB store with an optional copy-on-write page shadow
  (powers reverse-step). `SaveFullState`/`LoadFullState`.
- `Instr`, `AddrMode`, `Opcodes`/`OpcodesCMOS` tables.
- `Snapshot` + `SnapshotRing` — per-step rewind ring (page deltas).
- Disassembly: `Disasm`, `DisasmCPU`, `DisasmWithSyms`, `WalkBack`.
- `Tracer` / `FileTracer` — per-instruction execution trace hook.

```go
ram := cpu.NewRAM()
mmio := cpu.NewMMIO(ram)
c := cpu.NewVariant(mmio, cpu.VariantNES)
c.Reset()
for !c.Halted {
    c.Step()
}
```

## peripheral

Reusable MMIO devices.

- `TextOutput` — Apple-1-style write-to-buffer at a chosen address
  ($F001 by convention).
- `KeyboardInput` — data/status register pair ($F004/$F005).
- `Snapshotable` — opt-in save-state interface for a peripheral.

Both satisfy `cpu.Peripheral`; register them on an `MMIO`.

## symbols

Parses cc65 / ld65 `.dbg` files.

- `Table` — symbol ↔ address lookups.
- `SourceMap` — address → `SrcLoc` (`file:line`) + `Range` spans.
- `LoadDbg` / `LoadSourceMap` / `SiblingDbg(binPath)` (auto-detect the
  `<bin>.dbg` sibling).

## loader

- `Load(ram *cpu.RAM, path string, opt Options) (*Result, error)` —
  loads `.bin` / `.prg` / `.hex` / `.o` (runs `ld65` for unlinked `.o`)
  into RAM. `Result` reports the load + entry addresses.

## expr

The watch / breakpoint-condition expression language, shared by the
TUI and the DAP server so both evaluate identically.

- `Compile(src string, syms *symbols.Table) (EvalFn, error)`
- `EvalFn` — `func(*cpu.CPU, cpu.Bus) uint32`.

## trace

Replays a `-trace` log.

- `Replay` — parsed `[]Frame`; step/seek over a recorded run.
- `Frame` — per-instruction PC + register snapshot.

## dap

A Debug Adapter Protocol server — drives the CPU from any DAP client
(VS Code, nvim-dap, chippy's own TUI in attach mode).

- `NewServer(r io.Reader, w io.Writer) *Server` + `Serve()` — run the
  dispatch loop over a stream (stdio / TCP).
- `AttachExisting(cfg AttachConfig)` — attach to an already-running
  CPU (the shared-`cpuMu` model nessy + chippy `:dap` use).
- `Capabilities` — the advertised feature set (the
  `internal/dap/transcript_test.go` goldens pin this against drift).
- Request / response / event types + `ReadMessage` / `WriteMessage`
  for the Content-Length wire framing.

## What's NOT public

- `internal/tui` — chippy's Bubble Tea UI. Chippy-binary-only.
- `internal/nes/*` — the NES PPU / APU / cart / DMA / joypad. Moves to
  the standalone nessy repo (#351); not part of chippy's library
  contract.
