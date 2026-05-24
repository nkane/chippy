# chippy — Project Context Dump

> Snapshot of the running understanding of this project. Generated 2026-05-11.
> Treat this as a handoff document — anything not visible from `git log` or
> the code itself should live here.

---

## 1. Project Overview

**chippy** is a Go-based TUI 6502 emulator with a Bubble Tea + Lipgloss source-level debugger. It targets ca65/cc65 toolchain output (`.bin`, `.prg`, `.hex`, `.o` via `ld65`) and aims to feel like an interactive debugger (gdb/lldb/nvim-dap style) for hobbyist 6502 development.

- **Module:** `github.com/nkane/chippy`
- **Repo:** https://github.com/nkane/chippy (public, primary branch `main`)
- **License:** MIT (`LICENSE` in repo root)
- **Latest release:** v0.0.1 (installable via Homebrew tap)
- **Go version:** 1.26.2 in `go.mod`; CI uses `stable`

### Vision
A debugger-first emulator. Run a binary from ca65, see source lines beside disassembly, set breakpoints/watchpoints with nvim-DAP-style sigils, step backwards, inspect memory, and integrate real peripherals (MMIO).

---

## 2. Architecture

### Package layout
```
cmd/chippy/             # main binary entry point
internal/cpu/           # 6502 / 65C02 core, opcode tables, addressing, interrupts
internal/loader/        # .bin/.prg/.hex/.o loaders; invokes ld65 when needed
internal/symbols/       # cc65 .dbg parser (symbol table + source map)
internal/tui/           # Bubble Tea model, panels, breakpoints, watchpoints
internal/peripheral/    # MMIO peripherals (TextOutput @ $F001, KeyboardInput @ $F004/$F005)
example/                # ca65 sample programs + Makefile
docs/                   # mascot prompts, this file
.github/workflows/      # CI + release
```

### Core types
- `cpu.CPU` — registers, flag helpers, opcode dispatch, interrupt latches
  - Fields: `A,X,Y,SP,P byte; PC uint16; Cycles uint64; Bus Bus; Variant Variant; Halted bool; extraCycles int; opcodes *[256]Instr; irqLine bool; nmiPending bool; nmiPrev bool`
- `cpu.Bus` interface — `Read(addr uint16) byte; Write(addr uint16, v byte)`
- `cpu.RAM` — flat 64KB backing store
- `cpu.Instr` — `{ Mode AddrMode; Cycles int; PageAdd bool; Exec func(*CPU, uint16, AddrMode) }`
- `cpu.Variant` — `VariantNMOS` | `VariantCMOS65C02`; selects opcode table
- `tui.WBus` — wraps `cpu.Bus` to capture memory access for watchpoints
- `tui.MemBP` — memory breakpoint kinds (read / write / read+write)
- `symbols.Table` / `symbols.SourceMap` — parsed cc65 `.dbg` data

### Opcode tables
- `Opcodes [256]Instr` — NMOS, authoritative (`internal/cpu/opcodes.go`)
- `OpcodesCMOS [256]Instr` — initialised from `Opcodes` then overridden (`internal/cpu/opcodes_cmos.go`)
- Illegals patched into NMOS table by `opcodes_illegal.go` (runs after CMOS init due to lex file order)
- CPU dispatch goes through `c.opcodes[op]` so variant switching is free

### Step semantics
- `Step()` services interrupts at instruction boundary, THEN executes one opcode
  - NMI checked first (edge-triggered, always taken)
  - IRQ checked second (level-triggered, only when `FlagI` clear)
  - Servicing is 7 cycles, pushes PC+P (B clear), sets I, jumps to vector
  - Servicing un-halts the CPU
- Returns total cycles including interrupt overhead + branch extras
- `c.Cycles` is also advanced (same total)
- `c.extraCycles` is the side channel for branches and CMOS BCD; reset each Step

### BCD differences
- **NMOS:** A and C reflect decimal arithmetic; N/V/Z reflect the parallel binary path (a real 6502 quirk)
- **CMOS:** N/V/Z reflect the decimal result; +1 cycle penalty
- Implementation: ADC/SBC dispatch via `c.Variant` to `adcDecimalCMOS` / `sbcDecimalCMOS`

### Interrupts (PR #33, issue #10)
- `AssertIRQ()` / `ReleaseIRQ()` — level-triggered, sets/clears `irqLine`
- `TriggerNMI()` / `DeassertNMI()` — edge-triggered via `nmiPrev` rising-edge detect
- Service routines push `(P | FlagU) &^ FlagB` (B clear), then set FlagI, read vector ($FFFA / $FFFE)
- Wakes from `Halted` so a wait-loop can be interrupted by a peripheral

### Memory routing (PR #34, issue #16)
Bus chain: `CPU → tui.WBus → cpu.MMIO → cpu.RAM`
- `cpu.Peripheral` interface: `Range() (lo, hi uint16); Read(uint16) byte; Write(uint16, byte)`
- `cpu.MMIO` wraps an inner Bus, dispatches to registered peripherals first
- `internal/peripheral.TextOutput` — captures writes to $F001 into a buffer; rendered as a TUI panel
- `internal/peripheral.KeyboardInput` — Apple-1-style data/status register pair ($F004/$F005); TUI pushes keypresses, CPU reads & status drains
- Loader and reset-vector helpers write directly to `ram`, bypassing MMIO — peripherals must live at addresses no ROM will occupy

### Execution trace (PR #36, issue #21; #57 issue #43)
- `cpu.Tracer` interface — optional per-instruction hook on `CPU.Step()`. Methods: `LogStep`, `LogInterrupt`.
- `cpu.FileTracer` — buffered file sink (64 KiB), Enable/Disable/Close/SetPath
- CLI: `-trace PATH`; TUI: `:trace PATH | :trace on | :trace off | :trace`
- Instruction lines: PC, opcode bytes, disasm, A/X/Y/P/SP, cumulative CYC.
- Interrupt-entry lines: `---- NMI -> $FFFA (PC=$XXXX P=PP SP=SS CYC:N)` emitted at the service boundary, before the 7-cycle push/vector-load, so a reader sees where the PC jump in the next instruction originated.

---

## 3. Conventions & Workflow

### Branch & PR flow
- One issue → `feat/<short-name>` branch off `main`
- `gh pr create` with a body containing `Closes #N`
- CI must go green (3-OS test matrix + lint + klaus)
- `gh pr merge N --squash --delete-branch`
- File a new GitHub issue for any work that gets deferred

### Commits
- Conventional Commits: `feat:`, `fix:`, `docs:`, `ci:`, `test:`, `refactor:`, `chore:`
- These prefixes feed `.goreleaser.yml`'s changelog grouping

### GitHub CLI
- Authenticated as `nkane` over SSH (key: `~/.ssh/id_ed25519_github`)
- `workflow` scope confirmed (can edit `.github/workflows/`)

### Releases
- Cut a tag `vX.Y.Z` → `.github/workflows/release.yml` runs goreleaser
- Binaries published to GitHub releases
- Homebrew formula at `nkane/homebrew-tap` is auto-updated by goreleaser
- Secret: `HOMEBREW_TAP_GITHUB_TOKEN` (PAT)
- `homebrew-core` submission deferred until ~30 stars (issue #22)

### Quality bars
- `go build ./... && go test ./...` must stay green between increments
- TUI must stay responsive — every Update key path returns `tea.Cmd`
- Old persistence files (`~/.chippy/state-<rom>.json`) must keep loading

---

## 4. Progress

### Shipped
- **v0.0.1** released; brew install works via tap
- Release infra: `.goreleaser.yml`, `.github/workflows/release.yml`, MIT `LICENSE`, `nkane/homebrew-tap`

### Closed issues
- #1, #2, #3, #7, #8 (cycle audit), #9 (65C02), #10 (IRQ/NMI), #11–#15

### Merged PRs of note
- #23, #24, #26, #27, #28, #29 — earlier infra / features
- #30 — Klaus functional test harness (GPL ROM, downloaded on demand, sha256 verified)
- #31 — Cycle audit; introduced `extraCycles` side channel; fixes taken-branch undercount in `Step()` return
- #32 — Full 65C02 CMOS support (variant enum, table dispatch, ~30 opcodes, 3 new addr modes, JMP-IND wrap fix, CMOS BCD with +1 cycle, WDC NOP fill, `--cpu` flag, ca65 demo + e2e test)
- #33 — IRQ/NMI with edge/level semantics
- #34 — MMIO peripheral abstraction (issue #16); routing bus + Apple-1-style TextOutput ($F001) and KeyboardInput ($F004/$F005)
- #36 — Per-instruction execution trace (issue #21): `cpu.Tracer` hook on `Step()`, `cpu.FileTracer` (buffered 64K), `-trace PATH` CLI flag, `:trace PATH|on|off` TUI command
- #38 — CLAUDE.md "docs are part of every PR" rule: README/context/help-modal/exported docs move with code
- #39 — Stack panel JSR-frame annotation (issue #18): detects pushed return-address pairs via the `$20` opcode at `stored-2`; renders `ret $XXXX  callee  file:NN`; collapses non-frame runs; `T` toggles raw view
- #40 — Memory editor (issue #19): byte-level `MemCursor` (arrow keys, auto-scroll), `e` enters hex edit mode at cursor; 1–2 hex chars, Enter commits, Esc cancels; cursor persists in state file; `:goto` aligns view AND moves cursor
- #41 — Prompt history + tab-complete (issue #20): `~/.chippy/history` (cap 100, dedup, auto-save), Up/Down recall, Tab completes verbs and `:bp <symbol>` against the loaded `.dbg`, Ctrl-R reverse-incremental search (Ctrl-R again walks older). Added `symbols.Table.NamesWithPrefix`.
- v0.0.2 — release cut after #41. 7 features since v0.0.1; binaries + brew tap auto-updated.
- #54 — Reverse step (issue #17): `cpu.Snapshot` / `CPU.Snapshot`/`Restore` capture full regs + RAM + bookkeeping; `rewindRing` (cap 256, FIFO eviction, LIFO pop) records pre-step state on explicit-step paths only (free-run skipped to avoid 64 KiB/step cost); `<` pops one; status bar shows `rwd:N` depth.
- #55 — CMOS-aware disasm (issue #42): `DisasmCPU` / `DisasmCPUWithSyms` route through the CPU's opcode table so CMOS-only mnemonics (STZ/PHX/BRA/etc.) render correctly in the disasm panel, trace lines, and any future caller. Legacy `Disasm`/`DisasmWithSyms` retained as NMOS-default shims.
- #56 — `-run-on-start` flag (issue #44): start the CPU running instead of paused; pair with `-trace` for non-interactive capture.
- #57 — Trace interrupt-entry lines (issue #43): `Tracer.LogInterrupt` hook + `FileTracer` emits `---- NMI -> $FFFA (PC=... P=... SP=... CYC:...)` markers at the service boundary, so trace readers can spot the PC jump in the next instruction.
- #58 — Stack heuristic tightening (issue #45): `detectStackFrame` now also rejects frames whose stored return-address or JSR target falls below `codeMinAddr = $0200` (zero-page + stack-page). Cuts most false positives without losing real frames.
- #68 — Help modal paging: 4 pages, space/→ next, p/← prev, any other key closes. Splits the 10-section keybinding reference so the modal fits on small terminals.
- #69 — DAP transport + initialize/launch/disconnect (issue #47): `internal/dap` package with Content-Length framing, request/response/event types, server dispatch loop; `-dap stdio | tcp:PORT` CLI flag. Launches construct CPU+RAM+MMIO from `LaunchArguments` matching the CLI flag shape; capabilities advertise everything #48–#53 will eventually wire (conditional bp / instruction bp / disassemble / readMemory / writeMemory etc.).
- v0.1.0 — release cut after #69. Minor bump signals new DAP subsystem.
- DAP step controls (issue #50): `continue` / `next` / `stepIn` / `stepOut` / `pause` / `threads`. `continue` spins a background goroutine that calls `cpu.Step` until pauseRequested flips true or the CPU halts; emits `stopped` event on exit. Single-step variants refuse while running and emit `stopped` after the synchronous step.
- DAP stackTrace / scopes / variables / setVariable (issue #48): `stackTrace` walks JSR frames via `cpu.DetectStackFrame` (moved from tui/stack.go); scopes returns `Registers` + `Flags`; variables emits A/X/Y/SP/PC/P/Cycles for ref=1 and N/V/U/B/D/I/Z/C for ref=2; setVariable writes registers or toggles flags. Stack-frame detection moved from tui to cpu package as `cpu.DetectStackFrame`/`StackCodeMinAddr`.
- DAP breakpoints (issue #49): `setBreakpoints` (source-line, resolved via `srcMap.PCToSrc` reverse-lookup) and `setInstructionBreakpoints` (address). Both are destructive against their respective namespace per DAP spec. Run loop checks `bpHit` (flattened union) at each Step and emits `stopped` with reason=breakpoint.
- DAP disassemble / readMemory / writeMemory (issue #51): `disassemble` routes through `cpu.DisasmCPU` (variant-aware), reports `address`, `instructionBytes`, `instruction`, `symbol`, `location`/`line`; `readMemory`/`writeMemory` bypass MMIO so peripheral side-effects don't fire on debugger pokes. Base64 envelope per spec.
- DAP evaluate (issue #52): `evaluate` request compiles + runs the same expression grammar used by `:bp X if E`. Expression compiler moved from `internal/tui/cond.go` to a new `internal/expr` package so DAP and TUI share semantics (`expr.Compile`, `expr.EvalFn`); `tui.compileCondition` is now a thin wrapper.
- DAP example configs + onboarding docs (issue #53): `docs/dap.md` walkthrough, `examples/dap/launch.json` (VS Code) and `examples/dap/nvim-dap.lua` (nvim-dap). DAP-v1 epic complete.
- v0.2.0 — release cut after #77 / DAP-v1 epic.
- DAP stepBack (issue #79, first of #78 DAP-v2 epic): wires the rewind ring into DAP. Snapshot ring promoted from `internal/tui` to `internal/cpu` as `cpu.SnapshotRing` so both the TUI's `<` key and DAP's stepBack share storage. `supportsStepBack: true`.
- DAP setFunctionBreakpoints (issue #82, DAP-v2): symbol-name bps via `syms.LookupName`. New `bpsByName` map joins `bpsBySrc` and `bpsInst` in `rebuildBPHit`'s union. `supportsFunctionBreakpoints: true`.
- DAP loadedSources + source (issue #84, DAP-v2): editor's Loaded Scripts pane lists every file in `srcMap.Files`; the `source` request returns joined-line content with basename fallback for clients passing absolute paths. `supportsLoadedSourcesRequest: true`.
- DAP backward disassemble (issue #80, DAP-v2): `walkBack` promoted from `internal/tui` to `internal/cpu` as `cpu.WalkBack`; DAP's `disassemble` handler uses it for negative `instructionOffset`. Heuristic tightened to prefer earliest-start at equal sequence length, biasing toward real code boundaries.
- DAP completions (issue #85, DAP-v2): debug-console autocomplete returns registers (A/X/Y/P/SP/PC), flag bits (N/V/B/D/I/Z/C), and `.dbg` symbol names matching the cursor's trailing identifier prefix. `supportsCompletionsRequest: true`.
- DAP exception bps (issue #83, DAP-v2): `brk` filter advertised in initialize as `exceptionBreakpointFilters`. `setExceptionBreakpoints` flips `brkOnException`; run loop pauses before any `$00` opcode and writes `lastExceptionPC` for the `exceptionInfo` response. `supportsExceptionInfoRequest: true`.
- DAP bp condition/hitCondition/logMessage (issue #81, DAP-v2): every breakpoint family (source-line, instruction, function) honors the DAP modifier triple. New `bpMeta` per PC carries the compiled `expr.EvalFn`, hit target + running count, and an interpolating log template. `shouldFireBP` is the run-loop hit handler — logMessage emits an `output` event then continues without stopping.
- DAP integration test (issue #86, DAP-v2): `internal/dap/integration_test.go` under build-tag `integration`. Builds the binary, spawns `chippy -dap stdio`, drives initialize → launch → setInstructionBreakpoints → continue → variables → stackTrace → disconnect via an in-test JSON wire client. New `dap-integration` CI job runs it on every push.
- DAP attach v1 (issue #87, DAP-v2): `Server.AttachExisting(AttachConfig)` populates debuggee from an externally-built CPU/RAM/MMIO bundle without going through the loader. `attach` request now responds OK + emits stopped(entry) when a debuggee is wired. The TUI plumbing (`:dap PORT` command + shared CPU mutex) is deferred to #97.
- v0.3.0 — release cut after DAP-v2 push.
- Klaus 65C02 functional test (issue #59): `internal/cpu/klaus_cmos_test.go` runs against `65C02_extended_opcodes_test.bin` (download-on-demand + sha256-pinned). v1 skipped behind `CHIPPY_KLAUS_CMOS_STRICT` env because chippy's CMOS undocumented-opcode slots aren't WDC-spec'd yet (bug #99); test infrastructure is ready for #99's fix to be validated against.
- Exhaustive BCD test (issue #60): `internal/cpu/decimal_exhaustive_test.go` (build tag `decimal`) walks every (N1, N2, cin) through ADC and SBC in decimal mode for both variants — 524 288 cases total. Caught a real CMOS BCD bug on invalid-nibble inputs; fix applied to `adcDecimalCMOS` / `sbcDecimalCMOS` (Bruce Clark Appendix B algorithm). New `decimal` CI job runs the suite on every push.
- CMOS e2e CI (issue #61): new `cmos-e2e` workflow job installs cc65, builds `example/cmos_demo.bin`, runs the existing e2e test with `CHIPPY_CMOS_E2E_STRICT=1` so missing fixtures fail the build instead of silently skipping.
- CMOS NOP fills + interrupt D-clear (issue #99): WDC-spec NOP widths for undefined CMOS slots ($44=ZP, $54/$D4/$F4=ZPX, $DC/$FC=ABS, $5C=ABS-quirky 8-cycle). BRK / serviceIRQ / serviceNMI now clear D on CMOS variant (NMOS bug preserved). Klaus 65C02 functional test now passes end-to-end and runs unconditionally in CI.
- v0.3.1 — patch release after the CMOS correctness pass.
- `example/c/` — cc65-based C example programs (hello, sum, fizzbuzz) with shared `chippy.cfg` linker config + minimal `crt0.s` runtime. Builds via `make -C example/c`; runs via `chippy -rom example/c/<prog>.bin`. Source-map loader updated to prefer `.c` files over `.s` intermediates when both are recorded for the same PC, so the TUI source view (`v`) shows C source while stepping.
- Immediate window (issue #70): `I` opens a modal REPL backed by `internal/expr`. Each Enter evaluates the buffer against current CPU state, appends `expr → result` to scrollback. `↑` recalls the last expression. Result formatting matches DAP's evaluate response so both surfaces report identical values.
- Peripheral snapshots (issue #62): `cpu.Snapshot` grew a `Peripherals map[string][]byte` field; TUI and DAP both capture TextOutput buffer + Keyboard latch state into it on every push and restore on every pop. New `peripheral.Snapshotable` interface (Snapshot/Restore); both TextOutput and KeyboardInput implement it. Reverse-step across an MMIO write/read no longer desyncs the visible peripheral state.
- CoW RAM snapshots (issue #66): `cpu.Snapshot.RAM [0x10000]byte` is now `Pages map[byte][256]byte`. RAM gained an opt-in (`EnableShadow`) page-level write barrier that captures pre-write images. Two-phase capture protocol — caller takes the snapshot before the step, resets the shadow, runs the step (or multi-step sweep), then claims `snap.Pages = ram.TakeShadow()` and pushes. Typical 1-instr snap is ~hundreds of bytes vs 64 KiB before, so free-run now pushes on every step in both TUI tickMsg loop and DAP runLoop — reverse-step works across an unattended continue. 1000-iteration tight loop costs <1 MiB of total ring storage (validated by test).
- VS Code extension (issue #88): `extension/vscode-chippy/` — minimal TypeScript package that registers the `chippy` debug type and supplies a `DebugAdapterDescriptorFactory` that spawns `chippy -dap stdio`. `package.json` declares launch attributes, configuration snippets, and a `chippy.binaryPath` setting. `npm run package` produces an installable `.vsix`.
- WebAssembly playground (issue #67): `cmd/chippy-wasm/` builds a `js/wasm` binary that installs a `window.chippy` global (load / step / run / state / disasm / readMem / textOutput / pushKey / setVariant). `web/` ships the HTML/JS shell — `make -C web serve` builds + serves on :8080. Demos copy from `example/`. ld65/.o pipeline is explicitly out of scope (no shell-out in the browser); .bin / .prg / .hex parsing is inlined in the WASM main. New `wasm` CI job keeps the build target green. GitHub Pages auto-deploy via `pages.yml`.
- v0.4.0 — release cut after #62 / #66 / #88 / #67 ship.
- CPU correctness micro-audit (issue #122): WAI ($CB) and STP ($DB) were placeholder NOPs; now WAI halts until any IRQ/NMI (waking even on masked IRQ — falls through to next instruction without dispatching the handler) and STP halts permanently (new `stoppedBySTP` latch; only `Reset()` clears). Regression tests cover the halt/wake matrix plus IZP $FF zero-page wrap, PHP B/U push, IRQ B-clear push, and CMOS RTI D-restore.
- expr unary minus width-aware (issue #129): `-1` now evaluates to `$FF` instead of `$FFFFFFFF`; pick-smallest-power-of-two-width rule keeps `A == -1` matching a register holding `$FF`. Binary subtraction stays 32-bit modular by design. First-ever tests for `internal/expr/`.
- TextOutput bounded buffer (issue #128): `peripheral.TextOutput` now drops the oldest quarter when its buffer hits cap (default 64 KiB; `--text-buf-cap` overrides; `0` = unbounded). New `:textsave PATH` TUI command dumps the live buffer to disk. Prevents OOM on long-running programs and keeps reverse-step snapshots bounded.
- DAP advertised-but-missing gaps (issue #123): `supportsBreakpointLocationsRequest` was advertised but had no handler — now wired (line-granularity lookup against `srcMap.PCToSrc`). `launch.stopOnEntry` and `attach.stopOnEntry` are now `*bool`; explicit `false` auto-starts the run loop / suppresses the entry stopped event. `writeMemory.allowPartial=false` rejects overflowing writes instead of silently truncating.
- DAP input validation hardening (issue #124): `readMemory` rejects negative `Count`; `disassemble` clamps large-negative `Offset` and rejects negative `InstructionCount`; `evaluate` refuses while the run loop is in flight (was racing CPU/RAM reads); `stepOut` detects SP rises across the 8-bit wrap via signed-delta comparison; duplicate source-line and instruction breakpoints surface a `verified:false` `"duplicate ... — first entry kept"` message instead of silently overwriting.
- TUI help-modal + Tab completion polish (issue #127): help modal grew a "Prompt verbs" section listing every `:` command with concrete syntax (no more "guess the modifier syntax"). Tab completion extended beyond verb-only — `:trace on/off`, `:speed <hz>`, `:bp X <modifier>` (once/hits/if/log), and the new `:textsave` verb all complete from arg-pool. Symbol completion still works at arg-1 of address-taking verbs.
- State-file format freeze (issue #112): new `StateSchemaVersion = 1` written into every saved file. Loader treats absent version as v0 legacy (still decodes), `== 1` as current, `> 1` as silent ignore so an older chippy preserves a newer build's state. `internal/tui/testdata/state-v1.json` is the pinned golden; `TestLoadState_GoldenV1` fails when a tag or struct field changes incompatibly. `docs/state-format.md` documents the contract; `CLAUDE.md` cross-references it. Pre-existing bug fixed along the way: `loadMemBPs` was only called on the legacy-decode path, dropping memory watchpoints from any new-shape file.
- State-file content completeness (issue #125): `savedState` grew `DisasmFollow`, `StackAnnotate`, `InputMode`, `DisasmAnchor`, and `ImmediateHistory`. The two booleans serialize as `*bool` so a legacy v0 file's absence doesn't clobber the `New(c, r)` `true` defaults. Loader gates the new fields on `SchemaVersion >= 1` for the same reason. Golden file extended; new tests cover legacy-defaults-preserved and round-trip-of-additions.
- Release hardening (issue #130): goreleaser builds gain `-trimpath` + `-buildvcs=true` for reproducible / verifiable provenance; cosign keyless signing produces `*.cosign.bundle` per artifact (verify via `cosign verify-blob --certificate-identity=https://github.com/nkane/chippy/.github/workflows/release.yml@refs/tags/<TAG> --certificate-oidc-issuer=https://token.actions.githubusercontent.com`); syft emits SPDX SBOMs per archive; CI gained a `govulncheck` job that runs on every push; npm Dependabot now tracks the VS Code extension's deps; `SECURITY.md` documents the reporting flow + the hardening baseline.
- Docs hygiene (issue #131): README grew a "Why chippy" section with positioning vs py65 / lib6502 / visual6502; new `CONTRIBUTING.md` covers branch flow + commit style + quality bar + the "docs are part of every PR" rule; new `CHANGELOG.md` in Keep-a-Changelog format backfills v0.0.1 → v0.4.0 + an Unreleased section tracking the v1.0 epic; new `docs/editors.md` carries the editor-integration matrix.
- Perf baseline + CI regression gate (issue #113): `internal/cpu/bench_test.go` ships three benchmarks — `BenchmarkStep_NMOS`, `BenchmarkStep_CMOS`, `BenchmarkStep_WithSnapshot`. New `perfgate` build-tag test compares measured ns/op against `testdata/perf-baseline.json` and fails on >15% regression. New `perf baseline` CI job runs the gate on every push. Refresh procedure documented in `docs/perf-baseline.md`.
- NO_COLOR + colorblind themes (issue #126): new `internal/tui/theme.go` defines four palettes — `default`, `mono`, `protan` (red-green safe), `tritan` (blue-yellow safe). `NO_COLOR` env forces mono regardless of `--theme`. `:theme NAME` runtime command; persisted in the state file's new `theme` field; arg-completed by Tab. Help modal grew a Theme section. Tests cover env routing, applyTheme global swap, and round-trip persistence.
- WASM playground hardening (issue #132): new boot-error banner renders the underlying WASM-load failure (e.g. file:// MIME refusal) with a hint to use `make -C web serve`. CSP meta enforces `default-src 'self'` + `frame-ancestors 'none'` for clickjacking + script-injection defense. New `sw.js` service worker caches static assets for offline use. **share** button copies a `#rom=<base64>&format=&addr=&variant=` permalink (bytes stay client-side via URL fragment). Mobile-responsive: panes reflow to single column under 800 px.
- VS Code extension tests + disconnect docs (issue #133): `@vscode/test-electron` harness compiles `src/test/{runTest,suite/index,suite/extension.test}.ts`. Smoke tests cover presence + activation + manifest-declared debug type + the `chippy.binaryPath` setting. New `vscode-ext` CI job runs the suite under `xvfb-run`. `package-lock.json` is now committed so `npm ci` is deterministic. Extension README documents disconnect / crash handling and the test command.
- ca65 syntax highlighting (issue #117): TextMate grammar at `extension/vscode-chippy/syntaxes/ca65.tmLanguage.json` covers NMOS + 65C02 mnemonics, directives (`.proc`, `.segment`, `.byte`, `.if`, etc.), hex / binary / decimal literals, labels, comments, registers, operators. Files matching `.s` / `.s65` / `.asm` / `.inc` are auto-tagged. New `ca65.language-configuration.json` enables `;` comment toggling + bracket pairing. Snippets file ships reset-vector / `.proc` / `.ifdef` / halt-loop / Apple-1 `putc` templates. `.vsix` now ships 8 files, 7.37 KB.
- CMOS 65C02 cycle audit (issue #111): new `internal/cpu/cmos_cycles_test.go` exercises CMOS-only opcode cycles (BRA / INA / DEA / PHX / PLX / PHY / PLY / STZ / TRB / TSB / JMP (abs,X) / LDA (zp) / RMBx / SMBx / BBRx / BBSx), the BCD +1-cycle penalty under FlagD on ADC / SBC, the WDC NOP fills (1-byte/1-cycle defaults + the documented ZP/ZPX/ABS multi-byte slots + the quirky 8-cycle $5C), and WAI / STP. Surfaced two bugs in the CMOS opcode table: BRA was base-3 (computed as 4/5 instead of 3/4 because `branch()` adds +1 for always-taken), and the ZPX-prefixed NOP slots $54 / $D4 / $F4 were incorrectly routed through `case 0x04` and registered as 2-byte/3-cycle ZP NOPs instead of 2-byte/4-cycle ZPX NOPs. Both fixed; Klaus still green.
- DAP TUI attach (issue #97): new `:dap PORT` TUI command spawns the embedded DAP server in attach mode against the live CPU. `AttachConfig.CPUMu` carries a shared `*sync.Mutex` the DAP `dispatch()` and `runLoop()` take per iteration; `Model.step()` takes the same mutex when set. `Model.DAPListenAddr` surfaces the TCP address; `:dap` reports state, `:dap stop` closes the listener. `Model.SrcMap` retains the live `symbols.SourceMap` pointer so the embedded server can resolve source breakpoints. Race-detector test confirms `step()` blocks while the mutex is held.
- Linux distribution beyond brew (issue #118): goreleaser `nfpms:` block produces `.deb`, `.rpm`, and `.apk` packages per release. New `aurs:` block publishes `chippy-bin` PKGBUILD to AUR (gated on `AUR_SSH_PRIVATE_KEY` secret; skip-upload-auto so dev builds don't push). README install table covers Debian/Ubuntu (`dpkg -i`), Fedora/RHEL (`rpm -i`), Alpine (`apk add`), Arch (`yay -S chippy-bin`).
- Examples expansion (issue #119): four new ca65 demos. `mul16.s` (16x16 → 32-bit shift-add multiply, ZP state, ADC carry propagation), `echo.s` (Apple-1 I/O — poll $F005, read $F004, echo to $F001), `timer_irq.s` (IRQ vector + RTI handler alongside a busy main loop), `guess.s` (interactive number-guess state machine driven from MMIO keyboard). All build through the existing Makefile; README grouped under math / arithmetic / I/O / interrupts / CMOS categories with explicit watching tips.
- chippy.dev documentation site (issue #116): new `mkdocs.yml` configures MkDocs-Material; `docs/index.md` + `docs/quickstart.md` land as new pages alongside the existing reference docs. Pages workflow installs `mkdocs-material`, builds `docs/` to `_site/`, copies `web/` into `_site/playground/`, and uploads the combined artifact. Site root becomes the docs landing; `/chippy/playground/` is the WASM playground. `mkdocs build --strict` is part of the deploy gate.
- VS Code marketplace publish prep (issue #114): release workflow gains a `vscode-extension` job that syncs `extension/vscode-chippy/package.json` to the tag version (`npm version --no-git-tag-version`), `npm ci && npm run compile`, then `vsce publish --pat $VSCE_PAT`. Skipped on prerelease tags (anything with `-` in the name) since the marketplace can't unpublish. Extension README documents the PAT setup. `AUR_SSH_PRIVATE_KEY` secret is now also passed through so the AUR upload from #118 fires on real releases.
- Trace replay (issue #64): new `internal/trace` package parses chippy's `-trace` output back into a navigable `[]Frame`. `cmd/chippy --trace-replay PATH` opens the TUI in replay mode — `s` advances a frame, `<` rewinds, the CPU's regs are synced from the active frame so every panel renders as if paused at that PC. Help-modal grew a "Trace replay" section. Tests cover parse-basic, step+seek, malformed/empty lines.
- CPU bus-ticker hook (issue #175, nessy v0.1 spike): new `cpu.Ticker` interface; `cpu.Step()` invokes `Tick(cycles)` after every instruction. Cached at `c.busTicker` via `c.SetBus(b)` so the no-ticker fast path is a single nil-check (`BenchmarkStep_NMOS` stays ~8 ns/op, `BenchmarkStep_WithTicker` adds <1 ns). `MMIO` fans out to peripheral Tickers + forwards to its Inner bus's Ticker. `tui.WBus` forwards to its inner. Foundation for the nessy PPU / APU.
- CPU VariantNES (issue #174, nessy v0.1): Ricoh 2A03 variant — NMOS opcode table, but ADC / SBC ignore FlagD even when set. FlagD itself still toggles via SED / CLD / PHP / PLP for programs that probe it; only the BCD adder is missing in silicon. `-cpu nes` / `cpuVariant: "nes"` (DAP launch) / `chippy.setVariant("nes")` (WASM) all accept the new variant. Klaus 6502 functional test untouched; perfgate ceilings hold.
- nes/ines loader (issue #173, nessy v0.1): new `internal/nes/` package. `Parse(io.Reader)` / `ParseBytes([]byte)` consume an iNES (or NES 2.0) file into a `*ROM{Mapper, Mirroring, Battery, Trainer, PRG, CHR, NES2}`. Header validation rejects bad magic, zero PRG-bank claims, truncated bank data. Mapper byte assembled from flag6 high nibble + flag7 high nibble; mirroring honors the four-screen override. NES 2.0 header detected via flag7 bits 2-3 = 0b10 (extensions are best-effort parsed for v0.1). Tests cover NROM happy path, mapper-byte assembly (NROM, MMC1, MMC3, AOROM, GxROM), mirroring matrix, battery flag, trainer, CHR-RAM, NES 2.0 detection, 5 malformed-input rejections.
- nes/cart NROM (issue #176, nessy v0.1): new `internal/nes/cart/` subpackage. `Cartridge` interface (`CPURead/CPUWrite/PPURead/PPUWrite/Mirroring`); `Open(*nes.ROM)` factory dispatches by mapper number. v0.1 ships mapper 0 (NROM) only — 16 KiB carts mirror $8000-$BFFF to $C000-$FFFF, 32 KiB carts map directly. CHR-RAM variant (rom.CHR nil) allocates 8 KiB and makes PPU writes effective; CHR-ROM carts silently drop writes. Tests cover 32K direct map, 16K mirror, unmapped-below-$8000, PRG-write-noop, CHR-ROM read-only, CHR-RAM round-trip, PPU-above-$1FFF-ignored, bad PRG size rejection, Open dispatch + unsupported-mapper error.
- nes/joypad (issue #178, nessy v0.1): new `internal/nes/joypad/` package. `Port` is a `cpu.Peripheral` claiming `$4016-$4017` with two `Controller`s. Strobe-line model: a write to `$4016` bit 0 latches both controllers' shift registers; reads from `$4016` / `$4017` shift out A, B, Select, Start, Up, Down, Left, Right in that order. While the strobe is held high reads return the live A-bit continuously; after the eighth read the register is drained and reads return 1 (open-1 silicon). Host-side `Set(Button, pressed)` mutator drives live state; Ebiten input mapping will wire up in #179 when `cmd/nessy` lands. Tests cover shift order, strobe-high continuous read, latch-at-strobe snapshot semantics, P1/P2 independence, and $4017 write isolation.
- nes/ppu (issue #177, nessy v0.1): new `internal/nes/ppu/` package. `PPU` is a `cpu.Peripheral` claiming `$2000-$3FFF` (8-byte register window mirrored). 2 KiB internal VRAM with cart-driven horizontal / vertical / four-screen mirroring; 256 B OAM; 32 B palette RAM with `$3F10/$14/$18/$1C` hardware mirror to `$3F00/$04/$08/$0C`; pattern-table reads / writes routed to the cart's PPU bus. `Tick(cpuCycles)` advances 3 dots per CPU cycle through the 341 × 262 NTSC frame; vblank flag flips at scanline 241 dot 1 and raises NMI if PPUCTRL bit 7 is set (incl. the 2C02 quirk where setting bit 7 mid-vblank fires an immediate NMI). `$2007` reads are buffered (palette reads bypass); `$2006` / `$2005` share the standard write-toggle. Background-only renderer fires at vblank entry: walks 30 × 32 tiles, fetches nametable + attribute + pattern, emits 256 × 240 RGBA into `FrameBuffer()` using the embedded 64-color 2C02 palette. Out of scope for v0.1 (deferred to v0.2+): sprites, sprite-0 hit, mid-frame scrolling (v/t/x/w latch model), `$4014` OAMDMA (needs a CPU-stall hook that doesn't exist yet), greyscale / color emphasis, pre-render dot skip. Tests cover vblank timing + NMI gating, status-read clears vblank + latch, late-NMI quirk, VRAM r/w with auto-increment 1 and 32, palette buffer bypass, scroll latch toggle, palette mirror, horizontal + vertical nametable mirroring, OAMDATA cursor bump, mirrored-register-window decode, and synthetic-CHR uniform-background render against the 2C02 palette table.
- DAP client + `-dap-attach` Phase A (issue #180, nessy v0.1, partial): new `dap.Client` (Dial / NewClient / Initialize / Attach / Disconnect / Request / Events / Close) — the editor-side counterpart of the existing `dap.Server`. Read goroutine demuxes wire bytes into per-seq response channels and a buffered events fanout. `chippy -dap-attach tcp:HOST:PORT` drives the initialize+attach handshake, prints the server's advertised capabilities + initial events, then disconnects cleanly. Mutually exclusive with `-rom` / `-dap`. Phase B (introduce `CPUSource` interface, retrofit local mode) and Phase C (wire a DAP-backed source under attach so step keys/breakpoints drive the remote) are tracked separately and follow in their own PR — the TUI does **not** run in attach mode yet, since it can't drive a remote CPU until the refactor lands. Client tests use in-process `net.Pipe` to drive a real `dap.Server` through Initialize+Attach+Disconnect, concurrent-request demux, post-close request fail-fast, and `parseDialAddr` parsing.
- cmd/nessy (issue #179, nessy v0.1): new `cmd/nessy/` binary — the NES emulator entry point that ties together iNES loader (#173), NROM cart (#176), CPU VariantNES (#174), PPU (#177), and joypad (#178) under an Ebiten game loop, with chippy's DAP server attached on TCP for remote TUI control. Build-tagged `nessy` because Ebiten requires X11 / GL dev headers on Linux that the default CI runners don't carry — a `!nessy` stub in `main_stub.go` prints build instructions so `go build ./...` stays green. Wiring lives in `wiring.go` (untagged, unit-testable on every platform): the construction order is cart → MMIO over RAM → register cart-CPU-side wrapper ($4020-$FFFF) + joypad ($4016-$4017) → CPU on MMIO (Reset reads the cart's $FFFC vector through the registered wrapper) → PPU on cart + CPU → register PPU on MMIO → final Reset. Game loop runs ~29830 CPU cycles per Update (1.789773 MHz ÷ 60 fps), polls keyboard → joypad (Arrows / Z / X / Enter / Right-Shift = D-pad / A / B / Start / Select), and blits `ppu.FrameBuffer()` to the Ebiten screen via `WritePixels` with `Layout(256, 240)` for Ebiten-managed integer scaling. DAP listener runs in a goroutine via `AttachExisting` with a shared `cpuMu` mutex — same pattern chippy's `:dap` command uses. Flags: `-rom PATH` (or positional), `-dap-port N` (default 14785, 0 to disable), `-scale N` (default 3), `-mute` (no-op until APU lands in v0.2). Wiring tests verify cart-driven reset-vector wiring, CPU → PPU register reach via MMIO, and the cart's $4020-$FFFF range claim. Known v0.1 limitation: when a DAP client issues `continue`, the server's run loop and the game loop both call `Step()` under the mutex, double-stepping. Real pause/run gating with an atomic `Paused` flag is a v0.2 polish item.
- nestest golden-PC walk (issue #181, nessy v0.1): new `cmd/nessy/nestest_test.go` runs the canonical `nestest.nes` headless test at PC=$C000, single-stepping the live CPU against `nestest.log` (Nintendulator reference) and asserting PC + A + X + Y + P + SP match line-by-line. Fixtures downloaded on first run from `https://www.qmtpro.com/~nes/misc/` and cached under `$XDG_CACHE_HOME/chippy-tests/` with SHA-256 pins (`f67d55fd…` ROM / `627c8e18…` log); `CHIPPY_NESTEST_BIN` / `CHIPPY_NESTEST_LOG` env vars override with a local path. Build-tagged `nestest` (mirrors `klaus`); new CI job `nestest` on ubuntu-latest. Validates the iNES → cart → MMIO → CPU + PPU integration end-to-end — the headless run passes 8991 instructions with zero divergences against the golden log. Closes the v0.1 acceptance criterion that "nestest's PPU-touching opcodes don't trip" (#177) and "nestest passes end-to-end in CI" (#182).
- nessy demo: hello-bg (issue #194, epic #193): new `roms/demos/hello-bg/` — first homemade nessy demo. Static title screen renders "HELLO NESSY" centered on the nametable. Hand-rolled ca65 source + ld65 NROM-128 config + 8 KiB inline CHR-ROM with glyphs at their ASCII tile indices. Built `.nes` committed so toolchain installation isn't required. `roms/demos/Makefile` builds via `make hello-bg` (cc65 toolchain). Test driver `cmd/nessy/demo_test.go` boots the ROM through `buildNES` headlessly, advances 5 frames (driving the PPU manually past the `JMP self` halt detection so the renderer catches post-init state), hashes the framebuffer, and compares to a pinned SHA-256 (`helloBGFrameSHA`). Inspect variant (`CHIPPY_DEMO_INSPECT=1`) dumps a textual screenshot. Validates full PPU pipeline end-to-end with a real hand-rolled ROM: palette write via $2006/$2007, nametable + attribute clear loop, tile string write, BG-show enable, vblank-wait spin loops, reset vector wiring, and the NROM cart's $4020-$FFFF CPU-side claim. Along the way exposed a CPU bug: the chippy-debugger heuristic that flips `c.Halted = true` on `JMP self` was wrong for NES code (legitimate NMI-driven idle pattern stalls the bus-ticker fan-out → PPU freezes). Fix: `c.Variant != VariantNES` gate; NMOS/CMOS behavior unchanged.
- nessy demo: vblank-bounce (issue #196, epic #193): new `roms/demos/vblank-bounce/` — third homemade demo. Single 8×8 tile bounces inside the playfield, position updated by the NMI handler each frame. PPUCTRL bit 7 enables NMI; vblank @ scanline 241 raises it; the NMI handler erases the current cell, advances `(pos_x, pos_y)` with edge-clamped bounce in `[2, 29] × [2, 27]`, draws at the new cell, restores scroll, RTIs. Main loop is the canonical NES `JMP self` idle — exercises the CPU's variant-gated halt heuristic (NES skips the heuristic; KIL/STP still halt). Tests pin two framebuffer SHAs at 5 and 30 frames; assert they differ (catches a frozen NMI line / broken erase path). Inspect helper accepts `CHIPPY_DEMO_FRAMES` env to watch the tile walk frame-by-frame.
- nessy demo: input-echo (issue #195, epic #193): new `roms/demos/input-echo/` — second homemade demo. Eight indicator boxes in a controller layout (D-pad left, Select/Start/A/B right); each frame the program strobes `$4016` and reads 8 bits, flipping each indicator tile between empty (`$30`) and full (`$31`). Exercises joypad serial-shift reads, per-frame VRAM writes inside vblank, and scroll-reset after `$2006`. Tests cover two pinned framebuffer SHAs — idle (all empty) and ButtonUp-held (Up indicator full). Asserts the two SHAs differ to catch a broken joypad path. Inspect helper `TestDemo_InputEcho_Inspect` with `CHIPPY_DEMO_INSPECT=1` + optional `CHIPPY_DEMO_BUTTON` env renders a textual screenshot for any button combination.
- DAP attach Phase B+C (issue #180, nessy v0.1): new `internal/tui/source.go` introduces a `Source` interface that owns step / reset / continue / pause / breakpoint / step-back control flow + an async event stream. `LocalSource` (default) is a thin wrapper around `*cpu.CPU` + `*cpu.RAM`; `RemoteSource` (`source_remote.go`) wraps a `dap.Client` and translates each operation into a DAP request. Mirror model: the TUI's `m.CPU` + `m.RAM` stay populated in both modes so every display panel keeps reading the same fields — `RemoteSource` writes the mirror after every operation via `stackTrace` → PC and `variables(refRegisters)` → A/X/Y/SP/P/Cycles. Model retrofit: `Model.step()` now delegates to `m.Source.Step()`; `R` key, `r` key (run/pause), and `b` key (breakpoint toggle) all gate on `m.Source.Attached()` and route through `Continue / Pause / SetBreakpoints` when remote. `m.scheduleTick`'s run-loop skips local stepping in remote mode — the server's `stopped` event flips `m.Running` back off via a tea.Cmd that drains `m.Source.Events()`. `cmd/chippy -dap-attach tcp:HOST:PORT` now opens the full TUI against the remote: dial → initialize → attach → build mirror CPU + RAM → wrap with `RemoteSource` → `tea.NewProgram`. Phase A's smoke-test short-circuit is replaced. Closes #180. Tests cover Step/SetBreakpoints/Attached/Address against a real `dap.Server` over `net.Pipe`; `TestRemoteSource_Step_SyncsMirrorFromServer` verifies one wire step advances mirror PC + A to match server state.

- Source/disasm dual-mode scroll (issue #227): the `[ / ] / { / } / '` keys now route to the source panel when `ShowSource=true`, otherwise to the disasm panel (existing behavior). New `SourceFollow` (default true) + `SourceAnchorFile` + `SourceAnchorLine` mirror the disasm pattern: first scroll pins the anchor from the current PC's mapped source line and flips follow off; subsequent scrolls move the anchor by ±1 / ±8 lines, clamped to `[1, len(lines)]`. `'` restores follow mode. `sourceView` now picks centerFile/centerLine from anchor when pinned and still keeps the 👉 PC marker visible if PC's mapped file matches the centered file — so the user can scroll around without losing track of where execution is. Tests under `internal/tui/source_scroll_test.go` exercise the pin-on-first-scroll flip, anchor stepping, end-of-file clamps, pinned title hint, and follow restoration.
- VHS smoke tests (issue #231): new `test/smoke/` directory plus a root `Makefile` with `make smoke` / `make smoke-all` / `make smoke-clean` targets. Each `.tape` script drives chippy through a real TTY via [charmbracelet/vhs](https://github.com/charmbracelet/vhs), renders a `.gif`, and lets reviewers scrub the recording on PRs. Initial coverage: `chippy-source-scroll` (#227), `chippy-syms` (#226), `chippy-bp-and-run` (#225), `nessy-attach` (#220 / #224). A new `smoke` CI job on ubuntu-latest installs `ttyd` + `ffmpeg`, runs `make -C test/smoke chippy`, and uploads `test/smoke/out/*.gif` as workflow artifacts; the nessy tape is local-only because nessy needs X11 / GL dev headers the regular CI runners don't carry. CI gate is exit-code-only — a tape that crashes mid-render fails the job, but visual regressions still need human eyes on the artifact.
- CPU multi-source IRQ + APU frame-counter IRQ (issue #247, nessy v0.3): CPU gains `AssertIRQSource(name)` / `ClearIRQSource(name)` over an internal set of asserted sources; `irqLine` reflects a wired-OR of every active source. Existing single-source `AssertIRQ()` / `ReleaseIRQ()` remain as anonymous-source wrappers (source `""`) so the v0.1 / v0.2 tests don't regress. APU gains an `IRQSink` interface + `SetIRQSink()` + a `frameIRQFlag` field; 4-step mode fires the named IRQ source `"apu-frame"` at the end of each step-3 (not inhibited). `$4015` read returns the flag in bit 6 and clears both the flag and the source. `$4017` inhibit set clears any pending IRQ immediately. 5-step mode never fires the frame IRQ. `cmd/nessy/wiring.go` wires `ap.SetIRQSink(processor)` after both APU + CPU exist. Tests under `internal/cpu/irq_sources_test.go` cover OR semantics, idempotent assertion, anonymous + named source coexistence, and ghost-clear no-op; tests under `internal/nes/apu/irq_test.go` cover frame IRQ firing in 4-step, inhibit-clears-pending-and-prevents, 5-step skipping, `$4015`-read-clears, and headless-flag tracking. DMC IRQ wiring lands with the DMC channel itself (#246).
- nessy Ebiten audio sink (closes #207 follow-up): new `cmd/nessy/audio.go` (nessy build tag) wires the APU's int16 sample ring into an Ebiten `audio.Player`. `apuStream` implements `io.Reader`, draining `APU.Samples()` under `cpuMu`, duplicating each mono sample across L/R, and padding short reads with silence so the audio thread never blocks on a slow CPU. `audioSink` owns the context + player; nil-safe so `-mute` (or a failed init on a headless host) cleanly skips playback without affecting CPU/PPU. `game` gains an `audio *audioSink` field; `main` constructs the sink after building the bus and calls `sink.start()` before `ebiten.RunGame`. The APU keeps emitting samples regardless of mute state — only the player goes away — so save-state APU coverage works consistently.
- APU pulse 1 + pulse 2 + frame counter (issue #207, nessy v0.2): new `internal/nes/apu/` package. `APU` is a `cpu.Peripheral` claiming `$4000-$4013` (channel registers); `StatusPeripheral` wraps `$4015` so the discontiguous APU surface dodges the OAMDMA `$4014` window without forcing an MMIO refactor. Each `pulseChannel` (1 and 2) implements duty selector ($4000/4 bits 6-7), envelope unit (decay + constant + loop), sweep unit (period adjustment with the pulse-1 vs pulse-2 negate-mode difference), length counter (32-entry LUT), and the 11-bit period timer. The frame counter sits behind `SetFrameCounter` — joypad.Port now exposes `AttachFrameCounter(FrameCounterSink)` so `$4017` writes (which fall inside joypad's existing $4016-$4017 range) forward to the APU instead of being dropped. 4-step (240 Hz, default) and 5-step (immediate quarter+half tick on write) modes both supported; IRQ assertion deferred until v0.3 (no peripheral IRQ pump wired yet). Sample emission uses a fractional cycle-per-sample accumulator pinned to 44.1 kHz so the int16 ring stays locked over long horizons; linear pulse1+pulse2 mixer (real-silicon's non-linear DAC LUT is a v0.x quality knob). `cmd/nessy/wiring.go` registers APU + StatusPeripheral alongside cart + joypad + PPU + OAMDMA (6 peripherals total now). Tests under `internal/nes/apu/apu_test.go` cover Range split, $4015 disable clearing length, 5-step immediate tick, zero-crossing count matching expected pulse frequency (FFT-equivalent assertion), length-counter silencing, envelope decay, channel-enable gating, and the period<8 sweep-mute path. nestest + perfgate + Klaus + lint all green. Out of scope: Ebiten audio sink wiring (a host-side shim in cmd/nessy lands in a follow-up so headless tests stay process-bound-free), triangle/noise/DMC channels (v0.3), nesdev DAC mixer LUT, IRQ assertion through MMIO, save-state APU-restore-with-audio.
- Mid-frame scrolling (issue #206, nessy v0.2): PPU rendering goes per-scanline (was per-frame). New `scrollSnapshot {scanline, scrollX, scrollY, baseNametable}` captures `$2000` (nametable bits) + `$2005` + `$2006` writes during visible scanlines via `recordScrollChange()`; `stepDot` snapshots `frameStartScroll` when scanline rolls back to 0 (= scroll state at the start of the next frame, set during the just-ended vblank). `renderFrame` walks scanlines 0..239, advancing through `scrollEvents` and rendering each row with the active snapshot's scroll. New `renderScanline(y, snap)` rasterizes one row: per-pixel apply scrollX/scrollY, wrap into adjacent nametables at the 256 / 240 boundaries (PPUCTRL bits 0/1 XOR), then standard nametable + attribute + pattern fetch. A tiny single-tile cache cuts per-pixel busReads from 4 down to 1 within a tile row. `scrollFromV()` derives scroll values from the 15-bit `v` latch (nesdev "loopy" layout) so `$2006` mid-frame pairs (the SMB1 split mechanism) produce correct snapshots. Tests under `internal/nes/ppu/scroll_test.go` cover horizontal scroll shifting pixels, mid-frame split rendering top-32 rows with frame-start scroll + remainder with mid-frame scroll, horizontal nametable wrap, frame-start snapshot capture from `stepDot`, vblank-write filtering, and visible-write capture. v0.1 demos (hello-bg, input-echo, vblank-bounce) still render bit-identically (SHA-pinned tests unchanged). Out of scope: cycle-accurate per-dot v/t/x/w (fine-X bit slide, dot-257 horizontal copy, pre-render Y reload at dots 280-304) — per-scanline + per-tile fetch handles SMB1-class games, the per-dot work is a v0.x stretch.
- Sprite rendering (issue #205, nessy v0.2): the PPU's per-frame `renderFrame` now seeds a `bgOpaque [256*240]bool` mask alongside the RGBA framebuffer, then a new `renderSprites` (in `internal/nes/ppu/sprites.go`) composites the sprite layer on top. OAM walk visits all 64 sprites in index order — lower OAM index wins on per-pixel collision (real-silicon priority). Each sprite reads attr bits 0-1 (palette select), bit 5 (priority behind BG), bit 6 (hflip), bit 7 (vflip); 8×16 mode (PPUCTRL bit 5) doubles the per-sprite row count and routes the pattern table via tile-index bit 0 instead of PPUCTRL bit 3. Sprite-0 hit (`$2002` bit 6) latches whenever any opaque sprite-0 pixel coincides with an opaque BG pixel — gated by both BG show + sprite show. Sprite overflow (`$2002` bit 5) sets when any visible scanline crosses more than 8 sprites; the v0.2 implementation is the simple correct version, not the silicon "bug". Compositor still triggers from the `stepDot` vblank-entry path so v0.1 BG-only demos (hello-bg, input-echo, vblank-bounce) render bit-identically (SHA-pinned tests unchanged). Tests under `internal/nes/ppu/sprites_test.go` cover single-sprite render, sprite-0 hit fires/doesn't-fire, overflow at 9/8 sprites/scanline, priority-behind-BG, 8×16 mode bottom half, lower-OAM-index priority winning, and sprite-show-disabled suppressing all sprite-side effects. nestest + perfgate + lint all green. Out of scope: cycle-accurate sprite-0 hit timing, sprite-overflow silicon bug, color emphasis interactions, $2007-during-rendering quirks.
- CPU stall hook + $4014 OAMDMA (issue #204, nessy v0.2): new `cpu.Stall(int)` API plus a `pendingStall` field on `CPU`. `Step()` drains the full counter as one block — bus ticker fires once, `Cycles` advances, no opcode executes. NMI/IRQ service still preempts a queued stall so a peripheral that asserts an interrupt mid-DMA gets serviced first (matches the rest of the interrupt path). New `internal/nes/dma/` package with `OAMDMA` — a `cpu.Peripheral` claiming `$4014-$4014`. On write of byte `$XX`, walks 256 bytes from CPU page `$XX00-$XXFF` through the bus into the PPU's OAM cursor, then charges 513 stall cycles. Wired into `cmd/nessy/wiring.go` alongside cart + joypad + PPU; the `nesBus` struct gains a `dma` field for introspection. New PPU accessor `OAM(byte) byte` for side-effect-free test assertion. Tests cover `Stall`'s queue/drain/NMI-preempt semantics (`internal/cpu/stall_test.go`), the DMA peripheral via fakes + a real MMIO round-trip (`internal/nes/dma/oamdma_test.go`), and an end-to-end STA `$4014` against the live NES bus that verifies OAM contents + post-DMA stall drain (`cmd/nessy/wiring_test.go`). nestest + perfgate + Klaus all still pass. Out of scope for v0.2: the 514-cycle odd-CPU-cycle alignment penalty, DMC sample-DMA contention timing, per-byte sub-cycle accounting.

### Open issues
- #22 (homebrew-core) — blocked on stars
- Post-DAP follow-ups: #63 VIA 6522, #64 trace replay, #65 mem-watch conditional expressions

---

## 5. Key Decisions & Rationale

### Architecture
- **Variant-based CPU dispatch via per-CPU table pointer** — chosen over a runtime switch in every opcode so future variants (65816, etc.) only need a new table file. Tables share NMOS as a base and override.
- **CMOS table init via copy-then-override**, relying on Go's `init()` lex file ordering (`opcodes.go` < `opcodes_cmos.go` < `opcodes_illegal.go`). This is a load-bearing invariant — renaming files could break the init chain.
- **ZPR addressing handler self-fetches operand bytes** and self-advances PC; `resolve()` returns `(0, false)` for ZPR. Simpler than encoding both zp byte and rel target through `resolve`.
- **Disassembler is variant-aware** (PR #55, issue #42). Legacy `Disasm` / `DisasmWithSyms` still use the NMOS table for back-compat; `DisasmCPU` / `DisasmCPUWithSyms` route through `c.opcodes` so CMOS-only mnemonics (STZ, PHX, BRA, etc.) render correctly. TUI + trace switched to the CPU-aware path.

### Bug fixes worth remembering
- **PR #31:** `branch()` was mutating `c.Cycles` directly but `Step()` returned only `in.Cycles`. Result: taken branches undercounted return value by 1–2. Fix: `extraCycles int` field, reset each `Step`, folded into the return.
- **Test gotcha:** `r.Load(addr, prog)` then later `r.Write(addr, x)` clobbers the opcode. Discovered while writing the JMP (ind) wrap test — fixed by placing program at $8200 and using $8000 only as wrap-target sentinel.

### Tooling
- **CI matrix:** ubuntu + macos + windows × Go `stable`. Lint and Klaus jobs ubuntu-only. Coverage uploaded only from the ubuntu test job.
- **golangci-lint v2 syntax.** `errcheck` excludes `(*os.File).Close`, `bytes.Buffer` / `strings.Builder` writers, and `fmt.Fprint*` family.
- **`-covermode=atomic` is required with `-race`.** `fail_ci_if_error: false` on Codecov so transient upload failures don't break the build.
- **License: MIT.** GPL test ROMs (Klaus 6502_65C02_functional_tests) are NOT vendored — downloaded on demand with sha256 verification (`fa12bfc761e6f9057e4cc01a665a7b800ff01ae91f598af1e39a1201d01953fd`).

### UI
- **Sigils mirror nvim-DAP:**
  - 🛑 plain breakpoint
  - 👉 PC
  - 🔶 conditional
  - 💩 rejected
  - 📜 logpoint
  - 👁 read watch
  - ✏ write watch
  - 🔁 R+W watch
- **Wide emoji (2 cells) in marker column** → drop leading space to keep address column aligned.

---

## 6. Critical Context

### Local commands
```sh
# Standard build+test
go build ./... && go test -race -count=1 ./...

# CMOS-only tests
go test -count=1 -run 'TestCMOS|TestNMOS|TestVariant' -v ./internal/cpu/...

# Coverage
go test -race -count=1 -coverprofile=coverage.out -covermode=atomic ./...

# Lint
golangci-lint run ./...
golangci-lint run --build-tags=klaus ./...

# Klaus functional test (build-tagged)
go test -tags=klaus -timeout 5m -run TestKlaus -v ./internal/cpu/...

# Build example
make -C example cmos_demo.bin
make -C example run-cmos_demo
```

### CLI flags
```
chippy -rom <file> [-addr 0x8000] [-reset 0xADDR] [-cfg linker.cfg] [-dbg syms.dbg] [--cpu nmos|65c02]
```
- `-rom` — program to load (`.bin` `.prg` `.hex` `.o`)
- `-addr` — load address for raw `.bin` (default 0x8000)
- `-reset` — reset vector override (0 = use file's vector or load addr)
- `-cfg` — ld65 linker config; required for `.o` files
- `-dbg` — cc65 `.dbg` symbol file (auto-detected as `<rom>.dbg` if omitted)
- `--cpu` — `nmos` (default) | `6502` | `65c02` | `cmos` | `cmos65c02`

### Toolchain locations (macOS)
- `ca65`, `ld65`, `cc65` at `/opt/homebrew/bin/`

### References
- 65C02 opcodes: http://www.6502.org/tutorials/65c02opcodes.html
- 65C02 opcode matrix: http://www.oxyron.de/html/opcodesc02.html
- NMOS vs CMOS differences: http://wilsonminesco.com/NMOS-CMOSdif/
- Klaus 6502_65C02_functional_tests: https://github.com/Klaus2m5/6502_65C02_functional_tests

---

## 7. File Map (key files)

### CPU core
- `internal/cpu/cpu.go` — `CPU` struct, `Variant` enum, `New()` / `NewVariant()`, `Reset()`, `bindTable()`, interrupt API (`AssertIRQ`/`ReleaseIRQ`/`TriggerNMI`), service routines, flag helpers
- `internal/cpu/exec.go` — `Step()`, interrupt boundary service, addressing-mode load/store helpers, all opcode handlers (LDA/STA/ADC/SBC/branches/etc.)
- `internal/cpu/addressing.go` — `AddrMode` enum, `resolve()`; IZP/IAX/ZPR modes for CMOS; IND mode variant-branched
- `internal/cpu/opcodes.go` — NMOS opcode table (199 LOC)
- `internal/cpu/opcodes_cmos.go` — CMOS overrides (BRA, PHX/PHY/PLX/PLY, STZ, TRB, TSB, INA/DEA, BIT #imm, RMB/SMB/BBR/BBS, adcDecimalCMOS, sbcDecimalCMOS, cmosNOPs)
- `internal/cpu/opcodes_illegal.go` — NMOS unofficial opcodes (320 LOC)
- `internal/cpu/disasm.go` — disassembler; variant-aware via `DisasmCPU` / `DisasmCPUWithSyms`. Legacy NMOS-fixed `Disasm` still exported for callers without a CPU handy.
- `internal/cpu/memory.go` — `Bus` interface + `RAM` impl

### Tests
- `internal/cpu/cpu_test.go` — base helpers, LDA/ADC/etc. regression tests
- `internal/cpu/cycles_test.go` — 4 cycle-count regression tests (PR #31)
- `internal/cpu/cmos_test.go` — 15 CMOS regression tests
- `internal/cpu/cmos_e2e_test.go` — loads `example/cmos_demo.bin`, runs under CMOS, asserts state; self-skips when bin absent
- `internal/cpu/interrupts_test.go` — 10 IRQ/NMI tests (PR #33)
- `internal/cpu/klaus_test.go` — build-tagged Klaus harness (PR #30); pattern reusable for BCD/decimal suites

### TUI
- `internal/tui/model.go` — Bubble Tea model, run loop, panel layout, key bindings
- `internal/tui/wbus.go` — `WBus` wraps `cpu.Bus`, captures hits for memory watchpoints, ring buffer
- `internal/tui/bp.go` — breakpoints
- `internal/tui/cond.go` — conditional breakpoint expressions
- `internal/tui/membp.go` / `internal/tui/membp_test.go` — memory breakpoints
- `internal/tui/prompt.go` — command prompt
- `internal/tui/state.go` — persistence (`~/.chippy/state-<rom>.json`)

### Other
- `cmd/chippy/main.go` — CLI entry; flag parsing; bus wrap chain
- `internal/loader/` — `.bin`/`.prg`/`.hex`/`.o` loaders (ld65 invoked for `.o`)
- `internal/symbols/` — `.dbg` parser, symbol table, source map
- `example/Makefile` — `cmos_demo` target uses `--cpu 65c02`
- `example/cmos_demo.s` — `.setcpu "65c02"`; LDA/LDX/LDY/PHX/PHY/STZ/INC A/BRA/JMP self
- `.gitignore` — ignores `*.bin/*.o/*.dbg/*.prg/*.hex/*.lst/*.map`
- `.github/workflows/ci.yml` — 3-OS test matrix + lint + Codecov + `klaus` job (ubuntu-only)
- `.github/workflows/release.yml` — goreleaser on tag push
- `.goreleaser.yml` — multi-arch binaries + brew formula publish
- `nkane/homebrew-tap` repo — `Formula/chippy.rb` auto-updated by goreleaser

---

## 8. Next Steps (immediate)

1. Choose next from open issues: #17 (reverse step), #18 (stack panel), #19 (mem editor), #20 (prompt history). #22 (homebrew-core) is gated on ~30 stars.
2. **Deferred:** CI job for the CMOS e2e test (self-skips because binary is gitignored).
3. **Possible:** integrate Bruce Clark's BCD timing test or 6502_decimal_test as a klaus-style build-tagged suite — would also exercise the CMOS BCD path.
4. **User-side:** mascot image generation (prompts in `docs/mascot-prompts.md`).

---

## 9. Gotchas

- The `nkane/homebrew-tap` formula update flow requires the `HOMEBREW_TAP_GITHUB_TOKEN` secret to remain valid — rotate if expired.
- The Klaus ROM URL or sha256 changing would silently break CI's `klaus` job; pin is in `internal/cpu/klaus_test.go`.
- CMOS table init relies on file-lexicographic Go `init()` ordering. Renaming `opcodes_cmos.go` to come after `opcodes_illegal.go` would cause illegals to bleed into the CMOS table.
- `Step()` returns total cycles including interrupt service. Callers wanting just-the-instruction count would need separate tracking.
- `WBus` reads `c.PC` after `c.PC++`, so logged PC is one past the opcode for fetches. Tests assume this.
