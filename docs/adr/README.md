# Architecture Decision Records

One ADR per chippy release, capturing the architectural decisions made in that
version with their context and consequences. They're a reconstruction from the
git history, PRs, and `docs/context.md` — the canonical "why we built it this
way" log.

Each entry uses a compact form: **Context** (the forces), **Decision** (what we
chose), **Consequences** (what it bought / cost). Decisions that a later release
reversed or refined are marked *Superseded by* with a link.

## Index

| ADR | Release | Date | Theme |
|-----|---------|------|-------|
| [0001](0001-v1.0.0.md) | v1.0.0 | 2026-05-13 | Foundation — 6502/65C02 core, TUI debugger, cc65 toolchain |
| [0002](0002-v1.1.0.md) | v1.1.0 | 2026-05-15 | DAP server/client + attach; `VariantNES`; per-cycle bus ticker; monorepo |
| [0003](0003-v1.1.1.md) | v1.1.1 | 2026-05-16 | Remote-debug hardening; server-owns-CPU ownership model |
| [0004](0004-v1.2.0.md) | v1.2.0 | 2026-05-29 | Public 6502 library + semver; per-cycle CPU↔PPU; nessy carve-out |
| [0005](0005-v1.3.0.md) | v1.3.0 | 2026-06-04 | Debugger UX polish (epic #396) |
| [0006](0006-v1.4.0.md) | v1.4.0 | 2026-06-04 | DAP custom-request extension point (for nessy) |
| [0007](0007-v1.4.1.md) | v1.4.1 | 2026-06-04 | Remove VS Code extension (MS marketplace block) |
| [0008](0008-v1.5.0.md) | v1.5.0 | 2026-06-11 | DAP onramp + complete CPU ROM coverage + host debug hooks (epics #402, #419) |
| [0009](0009-v1.6.0.md) | v1.6.0 | 2026-06-16 | Accuracy tail (238/238 6502 bus-exact, 65C02 Tom Harte) + debugger UX (struct overlay, DAP array children, dirtyRanges) (epic #438) |
| [0010](0010-v1.7.0.md) | v1.7.0 | 2026-06-25 | TUI-via-DAP flip + freeze-beyond-RAM + WASM playground + full Tom Harte-validated 65816 core (epic #458) |
| [0011](0011-v1.8.0.md) | v1.8.0 | 2026-06-26 | Accuracy tail — 2A03 DMA-read open-bus seam (`DmaReadBus`, #481) + DMC-DMA steal timing: `idle()` polls the DMA halt + true-cycle `getCycle` (#493), converging `dmc_dma_during_read4` |
| [0012](0012-v1.9.0.md) | v1.9.0 | 2026-06-29 | Accuracy tail — per-cycle 65816 bus trace (`TestHarte65816BusTrace`, #495) |
| [0013](0013-v1.10.0.md) | v1.10.0 | unreleased | Bank-aware 24-bit bus for the 65816 (`Banked24`) — kill the bank-0 mirror; Intel HEX type-04, DAP/TUI bank-awareness, `:bank` (#505); cross-bank PBR-relative disassembly (#507) |

## Conventions captured across releases

- **One issue → `feat/<name>` branch → squash-merge `--delete-branch`.** Conventional Commits feed goreleaser changelog grouping.
- **Quality gate (every PR):** `go build ./...`, `go test -race -count=1 ./...`, `golangci-lint run ./...`, perfgate, docs updated in the same PR.
- **Releases:** bare `vX.Y.Z` tags → goreleaser (`.goreleaser.chippy.yml`); signed binaries + Homebrew/AUR/deb/rpm/apk + SBOMs.
- **GPL test ROMs are never vendored** — downloaded on demand + sha256-pinned (or ca65-ported from source).
