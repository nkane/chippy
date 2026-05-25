# chippy / nessy repo split

The plan + procedure for carving the nessy NES emulator out of the
chippy monorepo into `github.com/nkane/nessy` (#351). The shared
6502 core stays in chippy and is consumed as a library — that's why
it was promoted out of `internal/` (#349) and documented as a public
API (#350, [`api.md`](api.md)).

## Why split

chippy's CPU/bus/DAP core is mature + library-shaped; nessy is a
graphics app (Ebiten/GL deps) on top of it. Separate repos let:
- chippy ship as a clean 6502 library without dragging Ebiten in.
- nessy version + release on its own cadence.
- the chippy CI drop the X11/GL/build-tag complexity nessy needs.

## What moves where

| Stays in chippy | Moves to nessy |
|---|---|
| `cpu`, `dap`, `peripheral`, `expr`, `loader`, `symbols`, `trace` (public) | `cmd/nessy{,-wasm,-record}` |
| `cmd/chippy`, `internal/tui` | `internal/nes/*` (apu, cart, dma, joypad, ppu, ines, timing) |
| chippy demos (`example/`), docs | `roms/demos/`, `web/nessy/`, `docs/nessy/` |
| chippy release + pages workflows | nessy release workflow + smoke recorder + accuracy suite |

After the split nessy's `go.mod` requires `github.com/nkane/chippy`
at a pinned version; its core imports
(`github.com/nkane/chippy/cpu` …) already use the public paths so
they need no rewrite. Only the nessy-internal imports
(`…/internal/nes/*`) get re-pointed to `github.com/nkane/nessy`.

## Procedure

### 1. Cut a chippy library release
nessy must pin a real chippy version. Tag chippy first:
```sh
git checkout main && git pull
git tag v1.1.0        # follow semver; this is the library version nessy pins
git push origin v1.1.0
```

### 2. Create the empty nessy repo
On GitHub: new repo `nkane/nessy`, no README/license (the carve
brings them).

### 3. Run the carve script
```sh
scripts/carve-nessy.sh v1.1.0 /tmp/nessy-carve
```
It clones chippy, `git filter-repo`s down to the nessy paths (history
preserved), rewrites `nessy-vX.Y.Z` tags → bare `vX.Y.Z`, re-points
the `internal/nes` imports, and writes the nessy `go.mod`.

### 4. Verify + push
```sh
cd /tmp/nessy-carve/nessy
go mod tidy
go build -tags=nessy ./... && go test -race ./...
git remote add origin git@github.com:nkane/nessy.git
git push -u origin main --tags
```

### 5. Clean chippy
In a follow-up chippy PR, remove the carved paths + the nessy CI
jobs (the `nessy tagged build`, the smoke job's `nessy-record` step,
the `accuracy` job, the nessy release-workflow branch). chippy keeps
the core + TUI only.

### 6. Wire CI in the nessy repo
Port the nessy-specific jobs: tagged build (darwin/linux/windows),
the headless recorder smoke (#339), the accuracy ROM suite (#318),
the per-OS release pipeline (`nessy-vX.Y.Z` → now bare `vX.Y.Z`).

## Open question: `chippy -nessy ROM`

The one-shell debug launch spawns the nessy binary. Post-split,
chippy can't build nessy. Options:
1. Drop the auto-spawn; document "install nessy separately, then
   `nessy -wait-for-debugger ROM` + `chippy -dap-attach`".
2. Keep `-nessy` as a convenience that shells out to a `nessy` found
   on `$PATH` (no build dependency).

Lean #2 — the attach UX is nice + it's a thin exec wrapper.

## Status

Staged, not executed — gated on the new GitHub repo existing + a
chippy library tag. `scripts/carve-nessy.sh` + this doc are the
runnable plan.
