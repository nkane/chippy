# TUI-via-DAP migration pattern

Toward the v2.0 ambition — the TUI as a generic DAP client, the 6502 emulator
as library + DAP server — panels are migrated one at a time off direct
`cpu.CPU` field access and onto the DAP protocol. The **Registers panel**
(issue #394) is the proof of concept and the template for the rest.

## The shape

```
                 ┌──────────────────────────────┐
   regsView()  ──┤ m.Regs (RegSnapshot)         │   render: pure, reads cache
                 └──────────────▲───────────────┘
                                │ m.syncRegs() in Update
                 ┌──────────────┴───────────────┐
                 │ Source.Registers()           │   one DAP `variables` request
                 ├──────────────┬───────────────┤
        local ───┤ InprocClient │ wire Client   ├─── remote (-dap-attach)
                 └──────────────┴───────────────┘
```

1. **A snapshot type** holds exactly what the panel renders — `RegSnapshot`
   (`internal/tui/regs.go`): `A X Y SP P PC Cycles Halted`.
2. **`Source.Registers() (RegSnapshot, error)`** fetches it with a single DAP
   `variables` round-trip (the server's Registers scope already returns all
   seven values). Transport-agnostic via `remarshal`, which handles both the
   wire client's JSON body and the inproc client's Go struct.
   - `LocalSource` owns an in-process DAP server attached to the same CPU/RAM
     (the #393 inproc transport — sub-microsecond), so **local mode reads
     through DAP too**. In-process direct field access becomes dead code for
     this panel.
   - `RemoteSource` reuses its existing attach `*dap.Client`.
3. **`m.syncRegs()`** refreshes `m.Regs` in the Update loop — once per render
   tick, after key actions, and on seed (`New` / `WithSource`). Bubble Tea
   `View` stays pure: it renders the cached snapshot, never issues I/O.
4. **`regsView`** renders `m.Regs` — zero `cpu.CPU` access.

## Migrating the next panel

Repeat the four steps. For panels that need more than registers:

- Add a request (or reuse `variables` / `readMemory` / `stackTrace` /
  `disassemble`) that returns the panel's data in one round-trip.
- Add a `Source` method returning a snapshot struct; implement for both
  `LocalSource` (inproc) and `RemoteSource` (wire).
- Cache the snapshot on the Model; refresh it from `Update`.
- Render from the cache.

Keep both code paths alive until #461 flips the default (v1.7) — the in-process
direct access is the fallback / reference, and the DAP path is what the future
generic client uses. Disassembly and memory panels already have
`RefreshMemory` plumbing to build on.

## Migrated panels

- **Registers** (#394) — `variables` (Registers scope). The proof of concept.
- **Stack** (#449) — `stackTrace`. `Source.Stack()` / `fetchStack` /
  `m.syncStack()` / `StackSnapshot`. The DAP `stackTrace` response gained two
  additive chippy-extension fields so the snapshot can reproduce the panel's
  hardware-stack-page layout (not just a flat call list): `chippyStackAddr`
  (the `$01XX` slot of each pushed pair) and `chippyCallee` (the symbol at the
  JSR target, distinct from the frame `Name` = symbol at the return address).
  Frame detection / symbol / source-map lookups now live server-side
  (`cpu.DetectStackFrame` + the server's syms/srcMap); the panel only
  positions the snapshot frames over the page and renders the gaps as runs,
  with raw bytes still read from the DAP-fed RAM mirror (#451 formalizes that).
  Local mode pushes the symbol table + source map into its in-process server
  via `LocalSource.SetSymbols` (the symbols load after `New`).
- **Flags** (#450) — `variables` (Flags scope, ref=2). `Source.Flags()` /
  `fetchFlags` / `m.syncFlags()` / `FlagsSnapshot` (eight bools). The server
  decomposes `P` into N/V/U/B/D/I/Z/C bit values; `flagsView` renders the
  cached snapshot instead of bit-testing `cpu.CPU.P`. During a remote free-run
  the `chippy-state` event (#395) already carries the raw `P`, so the handler
  decomposes it client-side via `flagsFromP` — keeping the Flags panel as live
  as Registers without a per-frame round-trip (the Flags scope stays the
  authoritative source on stop).
- **Memory** (#451) — `readMemory` + #440 `dirtyRanges`. `Source.ReadMemory()`
  / `fetchMem` / `m.syncMem()` / `m.MemView` (a window snapshot anchored at
  `MemViewBase`, `memWindow` = 0x400 bytes). `memView` renders `memByte(a)`
  from the snapshot, never `m.RAM.Read`. Local mode fetches the window via an
  inproc `readMemory` (same RAM, but over the protocol); remote serves the
  window from the DAP-fed RAM mirror (reconciled by `RefreshMemory` on stop,
  updated by `dirtyRanges` during a run), so a remote free-run needs no
  per-frame round-trip — the `chippy-state` handler calls `refreshMemWindow`
  after applying the deltas. The memory *editor* write path (`memWrite` →
  WBus/CPU bus) is unchanged; remote writes are #454. `m.RAM` stays the
  render-backing mirror; the change is the panel reads its window through the
  Source.
- **Disassembly** (#452) — `disassemble`. `Source.Disassemble()` / `fetchDisasm`
  / `m.syncDisasm()` / `DisasmSnapshot` (instruction lines + anchor). `disasmView`
  renders text/symbol from the snapshot, applying its own PC/breakpoint markers
  and styling; the `walkBack`/`disasmAddrsAround` render path and its cache are
  gone. The server's `disassemble` now renders source-map data ranges as
  `.byte $XX` (step 1) so it matches the panel for any client. Both Sources own
  an inproc DAP server — LocalSource on the live core, **RemoteSource on its
  mirror** — so disasm follows the streamed PC during a remote run with no
  per-tick wire round-trip (the mirror is current via chippy-state regs + #440
  dirtyRanges). Symbols/source-map are pushed into both via the `symbolSink`
  interface.
- **Render/nav residuals** (#461) — the last direct `cpu`/`RAM` reads in the
  render + navigation paths: the Stack panel's raw byte/run rows now read a
  DAP-sourced stack-page snapshot (`StackSnapshot.Page` via `readMemory`), and
  `disasmScroll` moves its anchor by stepping the DAP disasm snapshot instead
  of `cpu.WalkBack`/`cpu.DisasmWithSyms`. `walkBack` + `Model.isDataAddr`
  deleted.

## Status: render path complete

All five panels (registers, stack, flags, memory, disassembly) **and** the
navigation paths are DAP-sourced — no direct `cpu.CPU`/`cpu.RAM` reads remain
in any render or nav path (#461).

The **control** path (run/step/breakpoints/mem-edit/watchpoints) still drives
`m.CPU`/`m.RAM` directly — that's the debugger engine, not rendering. Routing
it through the DAP server (the "DAP-only" control flip) is tracked separately
in **#471**: a v2.0-scale re-architecture with its own run-mechanism / locking
/ rewind tradeoffs. The rich TUI rewind ring is kept as the local engine
exception.

## Cost

The local DAP round-trip is the inproc transport from #393: ~0.34 µs for a
`variables` request — negligible next to a render tick. Remote is the wire
cost (~30 µs over a unix socket), still well inside a frame.
