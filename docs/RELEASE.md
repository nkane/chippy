# Release process

chippy ships a single binary (`chippy`) under bare `vX.Y.Z` tags via
goreleaser. The NES emulator that used to live alongside it (`nessy`)
moved to its own repo at
[github.com/nkane/nessy](https://github.com/nkane/nessy) post-v1.2.0;
its release process is documented there.

## Cutting a chippy release

```sh
git checkout main && git pull
git tag v1.3.0     # follow semver — chippy started at v1.0.0
git push origin v1.3.0
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
  --certificate-identity=https://github.com/nkane/chippy/.github/workflows/release.yml@refs/tags/v1.3.0 \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --bundle chippy_1.3.0_linux_x86_64.tar.gz.cosign.bundle \
  chippy_1.3.0_linux_x86_64.tar.gz
```

## Pre-tag checklist

- `go build ./...`
- `go test -race -count=1 ./...`
- `golangci-lint run ./...`
- README + docs/context.md current
- CHANGELOG implied via conventional-commit log; verify the auto-grouped sections look right by running `goreleaser release --snapshot --clean --config .goreleaser.chippy.yml` locally
