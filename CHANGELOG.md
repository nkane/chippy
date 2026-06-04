# Changelog

All notable changes are recorded here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/). Per-release
artifacts and the auto-generated commit log live on the
[GitHub Releases page](https://github.com/nkane/chippy/releases).

## [Unreleased]

The v1.0 readiness epic at [#121](https://github.com/nkane/chippy/issues/121)
tracks the remaining gap. Changes since v0.4.0:

### Added
- DAP: `AttachConfig.CustomRequestHandler` extension point — hosts can
  serve their own `vendor/command` requests over the same DAP connection
  (invoked from dispatch's fallback path under the CPU lock for coherent
  state reads). Unknown commands without a handler still return "not
  implemented". Lets nessy expose NES PPU / OAM / mapper debug state to
  the TUI panels without forking the protocol. (#416)
- CPU: WAI ($CB) and STP ($DB) now halt the CPU correctly instead of
  acting as NOPs. WAI wakes on any IRQ/NMI (including masked IRQ —
  falls through to the next instruction without dispatching). STP halts
  until external reset. (#122)
- TUI: persistent UI state grew DisasmFollow, StackAnnotate, InputMode,
  DisasmAnchor, and ImmediateHistory; the v0 legacy decode path
  preserves `New(c, r)` defaults. (#125)
- TUI: help modal lists every `:` command with example syntax; Tab
  completion now extends to `:trace`, `:speed`, `:bp X <modifier>`, and
  the new `:textsave PATH` command. (#127)
- DAP: `breakpointLocations` request implemented; `launch.stopOnEntry`
  and `attach.stopOnEntry` are now pointer-typed so `false` skips the
  entry pause / auto-starts the run loop. (#123)
- Peripheral: TextOutput buffer is capped at 64 KiB by default
  (`--text-buf-cap` overrides; `0` disables). `:textsave PATH` dumps
  the live buffer to disk. (#128)
- Documentation: `docs/state-format.md` documents the v1 state-file
  schema contract; `SECURITY.md` documents the private-advisory flow
  and hardening baseline.

### Fixed
- expr: unary minus is width-aware (`-1` = `$FF`, not `$FFFFFFFF`),
  so `A == -1` matches an 8-bit register holding `$FF`. (#129)
- DAP: `readMemory.Count` and `disassemble.InstructionCount` reject
  negative inputs; `disassemble.Offset` is clamped instead of wrapping
  uint16; `evaluate` refuses to read CPU state while a continue is in
  flight; `stepOut` detects SP rises across the `$FF→$00` wrap;
  duplicate breakpoints surface a `verified:false` message instead of
  silent overwrite. (#124)
- DAP: `writeMemory.allowPartial=false` rejects writes that overflow
  the 64 KiB address space instead of silently truncating. (#123)
- TUI: `loadMemBPs` is now called on the current decode path too —
  memory watchpoints survive save → reload. (#112)

### Security
- All release artifacts are signed via cosign keyless OIDC; verify
  with the recipe in `SECURITY.md`. (#130)
- SPDX SBOMs are emitted per archive via syft.
- Go binaries built with `-trimpath` and `-buildvcs=true` for
  reproducible / verifiable provenance.
- New `govulncheck` CI job runs on every push.
- Dependabot now tracks the VS Code extension's npm deps.

### Format
- `~/.chippy/state-<rom>.json` files now carry `schemaVersion: 1`.
  Loader accepts absent (legacy v0), `== 1` (current), and silently
  ignores `> 1`. (#112)

## [0.4.0] — 2026-05-12

### Added
- **Reverse step** runs across free-run: page-level copy-on-write RAM
  snapshots cost ~hundreds of bytes per step instead of 64 KiB, so the
  TUI tickMsg loop and DAP runLoop can push on every step. (#66 →
  PR #108)
- **Peripheral snapshots** alongside CPU + RAM snapshots, so reverse-
  step across an MMIO write/read no longer desyncs visible state.
  `peripheral.Snapshotable` is the contract; TextOutput and
  KeyboardInput implement it. (#62 → PR #107)
- **Immediate window** (`I` opens a modal REPL backed by `internal/expr`).
  Same evaluator powers DAP `evaluate`. (#70 → PR #106)
- **VS Code extension** scaffold at `extension/vscode-chippy/` registers
  the `chippy` debug type and supplies a `DebugAdapterDescriptorFactory`
  that spawns `chippy -dap stdio`. (#88 → PR #109)
- **WebAssembly playground** at https://nkane.dev/chippy/ — the same
  NMOS/65C02 core compiled to WASM, driven by an HTML/JS shell. GitHub
  Pages auto-deploy via `pages.yml`. (#67 → PR #110)

## [0.3.1] — 2026-05-12

### Fixed
- 65C02 NOP fills now match WDC spec widths ($44 = ZP, $54/$D4/$F4 =
  ZPX, $5C = quirky 8-cycle ABS, $DC/$FC = ABS). (#99)
- CMOS interrupt entry clears the D flag (BRK / serviceIRQ /
  serviceNMI). NMOS quirk preserved.
- CMOS BCD now follows Bruce Clark Appendix B exactly; exhaustive
  524 288-input sweep is clean. (#60)

## [0.3.0] — 2026-05-12

### Added
- **DAP v2 round** wraps up the protocol surface: stepBack,
  setFunctionBreakpoints, loadedSources + source, backward
  disassemble, completions, exception breakpoints, conditional /
  hit-count / log breakpoints, integration test in CI, attach v1.
  (#78 epic)
- **Klaus 65C02 functional test** + **CMOS demo end-to-end in CI**.

## [0.2.0] — 2026-05-12

### Added
- **DAP v1**: transport + initialize/launch/disconnect, step controls,
  stackTrace + scopes + variables + setVariable, breakpoints (source +
  instruction), disassemble + readMemory + writeMemory, evaluate.
  (#47–#53)

## [0.1.0] — 2026-05-12

### Added
- DAP transport + initialize/launch/disconnect first cut. (#47 → #69)

## [0.0.2] — 2026-05-11

### Added
- Help-modal paging (#68), stack JSR-frame annotation (#39), memory
  editor (#40), prompt history + Tab completion + reverse-i-search
  (#41), execution trace (#21).

## [0.0.1] — 2026-05-11

### Added
- Initial release: NMOS 6502 emulator, Bubble Tea TUI, ca65/cc65 toolchain
  support (`.bin` / `.prg` / `.hex` / `.o` via `ld65`), MMIO peripherals
  (Apple-1-style TextOutput + KeyboardInput), Klaus Dormann functional
  test in CI, Homebrew tap distribution.
