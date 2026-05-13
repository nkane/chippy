# nessy — NES emulator on the chippy core

> Living plan. Snapshot generated 2026-05-13. Edit as decisions land.

## TL;DR

**nessy** is an NES emulator built on chippy's 6502 core. Two
processes:

1. **`nessy`** — game process. Runs the NES at 60 fps, opens a graphical
   window, reads keyboard / gamepad input, plays audio. Also runs
   chippy's DAP server in attach mode on a TCP port.
2. **`chippy`** — debug process. The existing TUI runs in attach mode
   (`:dap` command, [#97](https://github.com/nkane/chippy/issues/97)
   already shipped) and drives breakpoints / single-step / variable
   inspection over DAP against the live nessy session.

Same CPU, same snapshot ring, same source-level `.dbg` debugging. The
graphics + audio + cartridge peripherals are nessy's job; the
debugger UI is chippy's.

## Why two processes

- **Different I/O domains.** Game wants a graphics window + sustained
  60 fps audio. TUI wants a terminal. Mashing both into one process
  forces a Bubble Tea + Ebiten cohabitation that doesn't exist
  cleanly today.
- **Decouple the surfaces.** A bug in the rendering layer can't crash
  the debugger. A long-running TUI doesn't block frame rendering.
- **DAP already does this.** chippy's DAP attach + `:dap` command
  ([#97](https://github.com/nkane/chippy/issues/97)) ships the
  serialization story we need. Editors (VS Code, nvim-dap) get a
  third client surface for free.

## Architecture

```
+-----------------+    DAP (TCP, JSON)   +------------------+
|     nessy       |<-------------------->|     chippy       |
|  (game proc)    |                      |   (TUI / DAP     |
|                 |                      |    attach mode)  |
|  Ebiten window  |                      +------------------+
|  Audio out                            
|  Controller in                        
|                                       
|  +-----------+                        
|  |   CPU     | ← chippy/internal/cpu
|  |   RAM     |
|  |   MMIO    |
|  |   PPU     | ← new internal/nes/ppu
|  |   APU     | ← new internal/nes/apu
|  |   Cart    | ← new internal/nes/cart (mappers)
|  |   Joypads | ← new internal/nes/joypad
|  +-----------+
+-----------------+
```

- Single `nessy` binary owns the emulator state. chippy's CPU + RAM +
  MMIO + snapshot ring are imported as Go modules.
- chippy's `dap.AttachExisting` (already exists) takes the live CPU /
  RAM / peripherals and exposes them over DAP. nessy starts the DAP
  listener at boot.
- Debug session: user runs `nessy game.nes` in one terminal, then
  `chippy --dap-attach tcp:localhost:14785` in another. Both reference
  the same in-process emulator state via DAP wire.

## Repo layout

**Decision: monorepo through nessy v0.3, then split.**

nessy lives inside the chippy repo as `cmd/nessy/` + `internal/nes/`
through v0.1 → v0.3 (NROM + MMC1 + UxROM playable, audio shipping).
By v0.3 the `internal/cpu` shape will have stabilised — we'll know
whether `BusTicker` is the right primitive or something subtler —
and we extract `nessy` into `github.com/nkane/nessy` against a
post-split `chippy v2.0` that promotes the runtime packages out of
`internal/`.

### Why monorepo for the bootstrap phase

- nessy's PPU needs a per-cycle `cpu.Step()` hook that chippy doesn't
  currently expose. Mono lets one PR change `internal/cpu` + add the
  PPU consumer atomically — no version-fight, no go.work juggling.
- `internal/cpu` is in `internal/` (Go forbids external import). To
  split now we'd need to either promote those packages to public
  paths immediately — committing chippy to Go-API stability during
  the noisiest design phase — or vendor / `go.work` our way through,
  both ugly.
- Cross-cutting changes during the design phase (e.g. "add bus-ticker
  + use it in PPU + adjust perfgate") stay one PR / one review /
  one bisect timeline.
- Single CI matrix, single goreleaser pipeline.

### Why split at v0.3

- Identity. chippy is the debugger; nessy is the NES. Separate repos
  signal that to users far better than one repo with a split mascot.
- Release cadence. chippy v1.2 shouldn't block nessy v0.4 (or vice
  versa). Tag streams diverge cleanly.
- Dependency hygiene. chippy's tarball stops carrying Ebiten +
  audio-mixing transitives. `homebrew-core` review path (#22) stays
  small.
- Public API. By v0.3 cpu / dap / peripheral / symbols / expr /
  trace / snapshot will all be exercised by both products plus the
  WASM playground; promoting them to public packages with a v2.0
  stability promise is a one-time tax we'd have paid eventually.

### Mechanical split recipe

When v0.3 lands:

```sh
# 1. Cut a chippy release that promotes packages from internal/ to
#    public paths and bumps to v2.0.0 (API stability commitment).
git mv internal/cpu pkg/cpu
git mv internal/peripheral pkg/peripheral
git mv internal/dap pkg/dap
git mv internal/symbols pkg/symbols
git mv internal/expr pkg/expr
git mv internal/trace pkg/trace
# update imports + tag v2.0.0

# 2. Extract nessy's tree into its own repo, rewriting history.
git clone --no-local . /tmp/nessy
cd /tmp/nessy
git filter-repo \
  --path cmd/nessy --path internal/nes \
  --path-rename cmd/nessy:cmd/nessy \
  --path-rename internal/nes:internal/nes
# 3. Push to github.com/nkane/nessy; depend on chippy v2.0.
```

Until then, the plan below is monorepo-shaped: PRs land in the
chippy repo, file paths assume `cmd/nessy/` + `internal/nes/`.

### Future three-way split (option, not commitment)

If a *third* emulator ever lands (Apple II, C64, 2600 — anything that
reuses the chippy CPU + TUI), the v0.3 two-way split (`chippy` +
`nessy`) gets re-evaluated as a three-way:

```
chippy/   — pure 6502 / 65C02 library. No UI. cpu / peripheral / dap /
            snapshot / symbols / expr / trace.
dippy/    — generic CPU-emulator TUI debugger. Bubble Tea, panels,
            breakpoints, watch. Drives any chippy.Bus or any DAP
            server.
nessy/    — NES emulator (chippy + nessy-specific PPU / APU / cart).
fizzy/    — hypothetical Apple II (chippy + Apple-specific peripherals).
            Reuses dippy's TUI for free.
```

The TUI's CPU-agnostic parts (panels, command line, watch, conditional
breakpoints, immediate window, theme, state-file) are already generic.
The CPU-specific bits (6502 disassembly, flag rendering, symbol
table) would move to chippy core as render hooks dippy calls.

**Trigger**: a second emulator + obvious TUI duplication. Default is
**don't split this far**. The cost of a third repo (third release
pipeline, third version-bump cascade, three-way cross-cutting PRs)
isn't worth paying unless there's an actual second consumer.

If/when it triggers, extraction is mechanical:

```sh
# from the v0.3+ chippy monorepo:
git filter-repo --path internal/tui --path-rename internal/tui:internal/dippy
# in a fresh dippy repo, depend on chippy@v2.x for the CPU library
# nessy's go.mod adds dippy as a second dependency
```

Until then, dippy is **YAGNI**. The TUI lives inside chippy.

## What we reuse from chippy

| chippy package        | nessy use                                                  |
|-----------------------|------------------------------------------------------------|
| `internal/cpu`        | 2A03 CPU (NMOS 6502 minus decimal-mode effects)            |
| `internal/cpu/MMIO`   | $0000-$FFFF bus routing — register new NES peripherals     |
| `internal/cpu/Snapshot` + CoW ring | Reverse-step over NES execution             |
| `internal/dap`        | DAP attach mode exposes nessy state to chippy TUI / editors |
| `internal/expr`       | Breakpoint conditions reference NES regs / RAM             |
| `internal/symbols`    | `.dbg` from cc65-targeted NES homebrew → source-level bp   |
| `internal/peripheral` | Pattern for new MMIO devices (`Snapshotable` interface)    |
| `internal/trace`      | nessy frames can dump traces; replay them with `chippy -trace-replay` |

## What nessy adds

### `internal/nes/`

- **`ines.go`** — parser for the [iNES](https://www.nesdev.org/wiki/INES)
  ROM format (PRG-ROM + CHR-ROM banks + header flags). Emits
  `Cartridge` ready for mapper construction.
- **`cart/`** — mappers. v0.1 ships only **mapper 0 (NROM)** since
  >half of the NES homebrew + every Donkey-Kong-era commercial cart
  uses it. v0.x adds MMC1 / MMC3 / UxROM.
- **`ppu.go`** — Picture Processing Unit. Renders 256x240 pixels at
  ~60 fps. Registers at $2000-$2007 + DMA at $4014. Internal state:
  VRAM (2 KiB name-table mirror), OAM (256 B sprite memory), palette
  RAM (32 B), pattern tables (via cartridge CHR-ROM).
- **`apu.go`** — 5-channel audio. v0.1 ships 2 pulse channels +
  triangle (the bulk of NES soundtracks). v0.x adds noise + DMC.
- **`joypad.go`** — two controller ports at $4016/$4017. Maps Ebiten
  key events to NES button bitfields.
- **`bus.go`** — wires CPU MMIO + PPU + APU + cartridge into the
  canonical NES bus topology.

### `cmd/nessy/`

- **`main.go`** — Ebiten game loop. Each frame: run CPU cycles
  proportional to elapsed real time, hand PPU the same cycle budget,
  blit PPU framebuffer to the Ebiten Game image, sample APU output.
  Controllers read from `ebiten.IsKeyPressed`. DAP listener spun up
  at boot (default port 14785).
- **`main_test.go`** — golden-frame test against a known iNES test
  ROM ([nestest.nes](https://www.nesdev.org/wiki/Emulator_tests)).

### `cmd/chippy/`

- **`--dap-attach ADDR`** flag (new) — opposite direction of `:dap
  PORT`. Connects out to a running nessy / chippy DAP server. Starts
  the TUI in attach mode against the remote state. Reuses the wire
  protocol of [#97](https://github.com/nkane/chippy/issues/97).

## CPU adjustments

The NES 2A03 is an NMOS 6502 with two real differences:

1. **No decimal mode.** ADC/SBC under FlagD ignore the flag and act
   like binary. chippy already has a `Variant`; add `VariantNES` (or
   reuse `VariantNMOS` and gate D-effects per-variant). Tests:
   include nestest's BCD-disabled checks.
2. **DMA stall on $4014 write.** Writing to the OAM DMA register
   halts the CPU for 513-514 cycles while it copies 256 bytes
   page-aligned into PPU OAM. The PPU keeps running. Easiest: the
   write handler advances `c.Cycles` by the stall count + signals the
   tick budget for the PPU.

CPU's `Step()` doesn't currently expose a per-cycle hook — PPU needs
it (PPU runs at 3× CPU speed; every CPU cycle = 3 PPU cycles). Add an
optional `cpu.BusTicker` callback that the CPU invokes after each
instruction with the cycle count; the bus implements it and fans out
to PPU + APU.

## DAP additions

Most of DAP-v2 already covers nessy's needs. New surface:

- **Custom request `nessy.frame`** — returns a single PPU framebuffer
  PNG so an editor can show the current rendering inline. Useful for
  debugging visual glitches without leaving VS Code.
- **Custom request `nessy.button`** — synthesizes a controller
  press/release, helpful for scripted breakpoint scenarios.
- **`stackTrace`** already works (NES uses the same 6502 stack).
- **`scopes`** grows a "PPU" scope listing $2000-$2007 + OAM + palette.

## Milestones

| Phase  | Scope                                                                                    | Acceptance                                                                                  |
|--------|------------------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------|
| **0.1** | iNES loader, NROM mapper, CPU + RAM + minimal PPU (background only), Ebiten window, controller input (P1), `nessy game.nes` runs Donkey Kong / Super Mario Bros title screen. No audio. | nestest.nes passes; SMB1 title screen renders. |
| **0.2** | PPU sprites + scrolling. Mid-frame register changes. APU pulse channels. SMB1 playable. | SMB1 first level controllable; sprite + background scroll without glitches.                  |
| **0.3** | APU triangle + noise. MMC1 + UxROM mappers. Save-state via chippy's snapshot ring. APU DMC for sample-driven sounds. | Zelda 1 + Metroid playable.                                                                  |
| **0.4** | chippy attach: `chippy --dap-attach tcp:localhost:14785` opens TUI in remote-attach mode. DAP custom requests (`nessy.frame`, `nessy.button`). | Set a source-line breakpoint in cc65 NES homebrew from VS Code, hit it during play.         |
| **0.5** | MMC3 (bank-switched majors — Mega Man, Punch-Out). Performance pass. Multi-controller. | nestest cycle-accurate; 60 fps with the debugger attached.                                  |
| **1.0** | Stability. Test suite (NEStress, blargg test ROMs). Mapper 4 / 7 / 11. Documentation. | Top-100 NES titles run without obvious regressions; CI gates against blargg.                |

## Open questions

1. **Graphics library**: Ebiten vs raylib-go vs SDL2 cgo binding.
   - Ebiten: pure Go, no cgo, runs in WASM too (potential bonus: nessy
     in the chippy playground 🎉). Slightly less performant than
     hardware-accelerated paths. **Recommended for v0.1.**
   - raylib-go: cgo, easier audio mixing, mature. Locks out WASM.
   - SDL2: native, cross-platform, oldest tooling. cgo dependency.
2. **Per-cycle vs per-instruction PPU tick.** Real NES is per-cycle.
   Per-instruction is faster and sufficient for most ROMs but breaks
   mid-instruction PPU register reads (rare). Decision: per-cycle
   from v0.1 because retrofitting later is expensive.
3. **Audio mixing**: Ebiten's audio package wants 44.1 kHz mono / 48
   kHz stereo PCM. APU naturally runs at 1.789773 MHz; need a
   downsampler. Standard pick: lowpass filter + linear decimation.
4. **DAP runs in the game goroutine or a sibling?** Sibling, with the
   shared `cpu.CPUMu` from #97. Game goroutine drives the emulator
   loop; DAP goroutine handles requests + run-loop steps. The mutex
   serialises them; under a continue, game loop yields to DAP.
5. **Reverse-step UX with audio**: snapshot ring captures RAM/CPU but
   not APU output buffer. Reverse-step needs to mute or replay audio
   from the snapshot's APU state. v0.x problem; v0.1 has no audio so
   moot.
6. **Save states**: extend chippy's snapshot ring to a named save —
   `:save N` / `:load N` in TUI, or hotkey in game window? Both,
   eventually.

## Initial PR slate (v0.1 epic)

These can be filed in chippy's tracker (monorepo decision) the
moment we commit:

1. `feat(cpu): VariantNES — NMOS minus decimal-mode effects`
2. `feat(cpu): bus-ticker callback for per-cycle peripheral fan-out`
3. `feat(nes): iNES ROM loader + NROM mapper`
4. `feat(nes): PPU register file + name-table rendering (background only)`
5. `feat(nes): joypad ($4016/$4017) + Ebiten input mapping`
6. `feat(nessy): cmd/nessy main with Ebiten game loop`
7. `test(nes): nestest.nes golden-PC walk`
8. `feat(cmd/chippy): --dap-attach flag (TUI remote attach)`

Tests + docs land alongside each.

## Naming + branding

- Binary: `nessy`
- Repo path (monorepo): `cmd/nessy/`, `internal/nes/`
- Default DAP port: 14785 (same as chippy's default, since you'd
  typically only run one at a time)
- Logo / mascot: TBD — keep chippy's mascot for the debugger UI;
  nessy gets its own when there's something visible.

## Out of scope (v1.x or never)

- **Famicom Disk System** — too niche.
- **Vs. System / PlayChoice 10** — arcade variants, different bus.
- **Network play** — neither chippy nor nessy are network-aware.
- **NES 2.0 header extensions** — defer until a ROM that needs it
  actually appears.
- **Cycle-accurate vs visible cycle-budget tradeoffs in DMA** — pick
  visible accuracy in v0.1, ship blargg-validated cycle-accurate
  later.

## References

- nesdev wiki: https://www.nesdev.org/wiki/Nesdev_Wiki
- nestest reference log: https://www.qmtpro.com/~nes/misc/nestest.log
- blargg test ROMs: https://github.com/christopherpow/nes-test-roms
- Ebiten: https://ebitengine.org/
- 6502 vs 2A03 deltas: https://www.nesdev.org/wiki/2A03

## Status

This is a plan, not code. Next concrete step before any PR lands:

1. Spike: write `internal/nes/ines.go` against a public-domain test
   ROM, confirm the parser shape feels right.
2. Spike: extend `cpu.Step()` with a one-line bus-ticker hook + a
   stub PPU that just counts cycles. Validate the callback overhead
   isn't visible to chippy's existing perfgate.
3. Once both spikes are happy, file the v0.1 epic + slate above.
