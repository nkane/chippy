# chippy — Claude instructions

Go-based TUI 6502 emulator + debugger (Bubble Tea, Lipgloss). Targets ca65/cc65 toolchain. See `docs/context.md` for architecture deep-dive.

## Authorship
- I am the sole author. **Do not** add `Co-Authored-By: Claude` trailers to commit messages.
- **Do not** add "🤖 Generated with Claude Code" footers to PR bodies.

## Branch & PR flow
- One GitHub issue → branch `feat/<short-name>` off `main`.
- Conventional Commits: `feat:`, `fix:`, `docs:`, `ci:`, `test:`, `refactor:`, `chore:` — these feed `.goreleaser.chippy.yml` changelog grouping.
- PR body ends with `Closes #N`.
- Squash-merge with `--delete-branch`. Defer follow-ups by filing new issues.

## Release tag scheme
- chippy ships under bare `vX.Y.Z` tags. Goreleaser via `.goreleaser.chippy.yml` (last released: `v1.6.0` — accuracy tail + debugger UX cleanup, epic #438: 238/238 6502 bus-exact, 65C02 Tom Harte, struct overlay watch, DAP array children, chippy-state dirtyRanges, goreleaser cask. v1.5.0 — DAP onramp + complete CPU ROM coverage, epic #402; host debug hooks, epic #419. nessy carved out into [github.com/nkane/nessy](https://github.com/nkane/nessy) post-v1.2.0; VS Code extension removed v1.4.1).
- Full process in [`docs/RELEASE.md`](docs/RELEASE.md).
- **Every release gets an ADR.** Cutting a tag includes adding `docs/adr/NNNN-vX.Y.Z.md` (next sequence number) capturing that release's decisions, and a row in `docs/adr/README.md`. The tag does not go out without it.

## Docs are part of every PR (not a follow-up)
Every PR ships with the documentation changes its diff implies. Update **in the same PR**, never as a separate cleanup:

- **`README.md`** — when CLI flags, install instructions, or user-facing behavior change. Flag tables, examples, and the feature list must stay accurate.
- **`docs/context.md`** — the architecture / progress / decisions handoff doc. When a PR merges, move the issue from "Open issues" to "Merged PRs of note", drop any "in progress" stubs that referred to it, and add an architecture section if the PR introduced a new subsystem (e.g. MMIO, trace).
- **TUI help modal** (`internal/tui/model.go` `helpModal()`) — when a new `:command`, keybinding, or panel ships.
- **Code-level doc comments** — when an exported type/function changes shape or contract.
- **`docs/adr/`** — when the PR embeds an architectural decision (a choice with trade-offs, a new abstraction / extension seam / pattern, a protocol or dependency change, or a reversal). Add or update an ADR (Context / Decision / Consequences) in the same PR; fold per-release decisions into that release's ADR. Mechanical fixes/refactors don't need one. ADRs are numbered `docs/adr/NNNN-*.md` with an index in `docs/adr/README.md`.
- **This file (`CLAUDE.md`)** — when load-bearing invariants change, or when a convention itself changes.

A PR that adds a feature without updating its docs is incomplete. Reviewers will flag it; pre-empt by updating in the same diff.

## Quality bar (must pass before commit)
- `go build ./...`
- `go test -race -count=1 ./...`
- `golangci-lint run ./...`
- Persistence files (`~/.chippy/state-<rom>.json`) follow the v1 freeze contract in [`docs/state-format.md`](docs/state-format.md). v1 writers include `schemaVersion: 1`. New fields stay optional inside v1.x; semantic changes or removals require bumping `StateSchemaVersion` + a migration. The `TestLoadState_GoldenV1` test pins `internal/tui/testdata/state-v1.json` — update both whenever the format changes.

## Code style
- No comments explaining WHAT well-named code already says. Only non-obvious WHY.
- No backwards-compat shims for in-tree code I control. Just change it.
- Prefer editing existing files over creating new ones.
- Keep TUI responsive — every `Update` key path returns a `tea.Cmd`.

## Load-bearing invariants (don't break without flagging)
- CMOS opcode table init relies on Go `init()` lex order: `opcodes.go` < `opcodes_cmos.go` < `opcodes_illegal.go`. Renaming these files breaks the CMOS table.
- `cpu.Step()` services interrupts at the instruction boundary, then executes one opcode. Returns total cycles (incl. branch extras via `extraCycles` and interrupt service).
- MMIO bus chain: `CPU → tui.WBus → cpu.MMIO → cpu.RAM`. The loader and reset-vector helpers write directly to `RAM`, deliberately bypassing MMIO.

## When in doubt
- Ask before destructive git ops (force-push, reset --hard, branch -D).
- Ask before scope creep — bug fixes don't need cleanup; one-shot tasks don't need helpers.
