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

### Open issues
- #22 (homebrew-core) — blocked on stars
- Post-DAP follow-ups: #59 Klaus 65C02, #60 BCD test, #61 CMOS e2e in CI, #62 peripheral snapshots, #63 VIA 6522, #64 trace replay, #65 mem-watch conditional expressions, #66 CoW RAM snapshots, #67 WebAssembly playground

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
