# chippy — Claude instructions

Go-based TUI 6502 emulator + debugger (Bubble Tea, Lipgloss). Targets ca65/cc65 toolchain. See `docs/context.md` for architecture deep-dive.

## Authorship
- I am the sole author. **Do not** add `Co-Authored-By: Claude` trailers to commit messages.
- **Do not** add "🤖 Generated with Claude Code" footers to PR bodies.

## Branch & PR flow
- One GitHub issue → branch `feat/<short-name>` off `main`.
- Conventional Commits: `feat:`, `fix:`, `docs:`, `ci:`, `test:`, `refactor:`, `chore:` — these feed `.goreleaser.yml` changelog grouping.
- PR body ends with `Closes #N`.
- Squash-merge with `--delete-branch`. Defer follow-ups by filing new issues.

## Quality bar (must pass before commit)
- `go build ./...`
- `go test -race -count=1 ./...`
- `golangci-lint run ./...`
- Persistence files (`~/.chippy/state-<rom>.json`) must keep loading — `savedState` schema is append-only.

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
