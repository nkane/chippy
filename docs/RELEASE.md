# Release process

chippy is a monorepo with two shippable binaries (`chippy`, `nessy`). Each project tags independently:

| Project | Tag pattern | Trigger |
|---|---|---|
| chippy | `vX.Y.Z`        | goreleaser flow → homebrew tap, AUR, .deb / .rpm / .apk, VS Code marketplace, cosign signatures, SPDX SBOM |
| nessy  | `nessy-vX.Y.Z`  | Custom per-OS build → darwin (amd64+arm64), linux (amd64), windows (amd64) binaries bundled with demo ROMs |

Both share `.github/workflows/release.yml`; the workflow gates each job on the tag prefix.

chippy uses the bare `vX.Y.Z` scheme it shipped with at `v1.0.0`. Goreleaser OSS doesn't support monorepo tag prefixes (pro-only), so the workflow distinguishes by `startsWith(github.ref_name, 'nessy-')` rather than per-project goreleaser configs.

## Cutting a chippy release

```sh
git checkout main && git pull
git tag v1.1.0     # follow semver — chippy starts at v1.0.0
git push origin v1.1.0
```

The release workflow:
1. Runs goreleaser via `.goreleaser.chippy.yml`.
2. Builds linux/darwin/windows × amd64/arm64 binaries (CGO off — pure Go chippy core).
3. Generates SPDX SBOMs via syft + cosign-signs every artifact via keyless OIDC.
4. Pushes Homebrew formula, AUR PKGBUILD, .deb / .rpm / .apk packages.
5. Publishes the VS Code extension (skipped for `-rc` / `-beta` / `-alpha` suffixes).

Verify a chippy artifact:
```sh
cosign verify-blob \
  --certificate-identity=https://github.com/nkane/chippy/.github/workflows/release.yml@refs/tags/v1.1.0 \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --bundle chippy_1.1.0_linux_x86_64.tar.gz.cosign.bundle \
  chippy_1.1.0_linux_x86_64.tar.gz
```

## Cutting a nessy release

```sh
git checkout main && git pull
git tag nessy-v0.1.0
git push origin nessy-v0.1.0
```

The release workflow:
1. Spawns 4 parallel build jobs (ubuntu / macos-13 / macos-latest / windows-latest).
2. Each runner installs platform graphics dev libs as needed and runs `go build -tags=nessy -o nessy ./cmd/nessy`.
3. Packages the binary + LICENSE + README + every demo under `roms/demos/` into a per-OS archive.
4. First job to finish creates the GitHub release (marked prerelease); the rest attach their artifacts.

Linux build deps the workflow installs:
```sh
sudo apt-get install -y \
  libgl1-mesa-dev xorg-dev libasound2-dev \
  libxcursor-dev libxinerama-dev libxi-dev libxrandr-dev
```

nessy releases are intentionally minimal for the v0.1 alpha — no homebrew tap, no AUR, no cosign / SBOM. Those land when nessy graduates (likely v0.2 once sprites + audio are real).

## Pre-tag checklist

For chippy:
- `go build ./...`
- `go test -race -count=1 ./...`
- `golangci-lint run ./...`
- README + docs/context.md current
- CHANGELOG implied via conventional-commit log; verify the auto-grouped sections look right by running `goreleaser release --snapshot --clean --config .goreleaser.chippy.yml` locally

For nessy:
- `make -C roms/demos all` (rebuild every demo from source)
- `go test -race -count=1 ./...` (demo SHA tests stay green)
- `go build -tags=nessy ./cmd/nessy` on darwin (cheapest check)
- Eyeball at least one demo via `CHIPPY_DEMO_INSPECT=1 go test ./cmd/nessy/... -v`
- Tag is `nessy-vX.Y.Z`, never bare `vX.Y.Z` (that's reserved for chippy)

## Future: the v0.3 monorepo split

`docs/plans/nessy.md` documents the planned chippy / nessy repo split at v0.3. The current scheme is already split-friendly:
- chippy stays on `vX.Y.Z`. Post-split chippy repo keeps its history and tag scheme unchanged.
- nessy's `nessy-vX.Y.Z` tags get rewritten to bare `vX.Y.Z` in the new nessy repo via `git filter-repo --tag-rename nessy-v:v`.
