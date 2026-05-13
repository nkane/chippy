# Contributing to chippy

Thanks for picking up a chippy issue. The project is small and the
conventions are short — this file tells you what good looks like before
you open a PR.

## Quick start

```sh
git clone git@github.com:nkane/chippy.git
cd chippy
go build ./cmd/chippy
go test ./...
```

A working ca65/cc65 install (`brew install cc65` on macOS, `apt install
cc65` on Debian/Ubuntu) unlocks the `example/` and `example/c/` build
targets but isn't required for editing the Go code.

## Branch & PR flow

1. Pick an issue (or open one and tag it).
2. Branch `feat/<short-name>` (or `fix/`, `docs/`, `ci/`) from `main`.
3. Make focused, tested changes.
4. Open a PR with a body that ends in `Closes #N`.
5. Wait for CI to go green.
6. Squash-merge with `--delete-branch` (project default).

CI must pass:

- `go build ./...`
- `go test -race -count=1 ./...`
- `golangci-lint run`
- `govulncheck ./...`
- The platform-specific harnesses on push: Klaus 6502 + 65C02, exhaustive
  BCD, DAP integration, cmos demo end-to-end, WASM build, and the perf
  gate (see [`docs/perf-baseline.md`](docs/perf-baseline.md)).

## Commit style

[Conventional Commits](https://www.conventionalcommits.org/) with these
prefixes:

| prefix     | when                                                       |
|------------|------------------------------------------------------------|
| `feat:`    | new feature or capability                                  |
| `fix:`     | bug fix                                                    |
| `docs:`    | documentation only                                         |
| `ci:`      | CI / release workflow                                      |
| `test:`    | adds or refactors test coverage                            |
| `refactor:`| non-behavior change                                        |
| `chore:`   | dep bump, tidying, version bumps                           |

These prefixes feed `.goreleaser.yml`'s changelog grouping — `feat:` and
`fix:` get their own sections on the release page.

**Do not** add `Co-Authored-By: Claude` trailers or "🤖 Generated with
Claude Code" footers; the project lead is the sole author of record.

## Quality bar

See [`CLAUDE.md`](CLAUDE.md) for the project's invariants and code-style
expectations. The short version:

- No comments explaining WHAT well-named code already says. Only
  non-obvious WHY.
- No backwards-compat shims for in-tree code I control. Just change it.
- Prefer editing existing files over creating new ones.
- TUI must stay responsive — every `Update` key path returns a
  `tea.Cmd`.
- Persistence files (`~/.chippy/state-<rom>.json`) keep loading — see
  [`docs/state-format.md`](docs/state-format.md) for the format-freeze
  contract.

## Docs are part of every PR

A PR that adds a feature without updating its docs is incomplete.
Reviewers will flag it; pre-empt by updating in the same diff:

- `README.md` — CLI flags, install path, user-facing behavior changes.
- `docs/context.md` — architecture / progress / merged-PR running log.
- `docs/dap.md` — DAP-related changes.
- `docs/state-format.md` — state-file schema changes (and bump
  `StateSchemaVersion` when format incompatible).
- TUI help modal (`internal/tui/model.go` `helpModal()`) — new
  keybindings, `:` commands, panels.
- Exported function / type docs — when shape or contract changes.

## Reporting bugs

For security issues, see [`SECURITY.md`](SECURITY.md) — please use
GitHub's private security advisory flow rather than a public issue.

For everything else, open a regular issue with:

- chippy version (`chippy --version` or commit SHA).
- Platform.
- A minimal reproduction (a small `.s` or `.bin` is ideal).
- Expected vs. actual behavior.

## What we want help with

- v1.0 epic: open issues tagged into the v1.0 readiness checklist at
  [#121](https://github.com/nkane/chippy/issues/121).
- Examples — the more end-to-end 6502 programs the `example/` directory
  has, the better the playground experience for newcomers.
- Editor integration matrix — if you wire chippy into your editor and
  it works (or doesn't), file an issue with the launch config.

## Re-recording the README onboarding GIF

The hero animation at the top of `README.md` is produced by:

```sh
docs/media/record-onboarding.sh
```

The script wants `asciinema` + `agg` on `PATH`. It records a ~25-second
scripted session into `docs/media/onboarding.cast` and renders the GIF
into `docs/media/onboarding.gif`. Both files are overwritten in place
— commit the new pair when the cast is clean.

Re-record after any UI change that's user-visible from the first
30 seconds: new keybinding, theme rework, panel reordering.
