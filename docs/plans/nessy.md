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

**Decision**: monorepo. nessy lives inside the chippy repo as
`cmd/nessy/` + `internal/nes/`. Rationale:

- Lets us evolve `internal/cpu` shape (e.g. adding bus-cycle hooks
  the PPU needs) without breaking a separate-repo consumer's `go.mod`.
- chippy is small enough that the build / test matrix can absorb it.
- A future split is trivial: `git filter-branch` extracts `cmd/nessy/`
  + `internal/nes/` + `go.mod` shim.

If/when nessy becomes its own surface with its own release cadence,
revisit.

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
