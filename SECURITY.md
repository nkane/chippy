# Security policy

## Supported versions

Only the latest **chippy** release line receives security fixes.

| Version | Supported |
|---------|-----------|
| latest (1.x) | yes |
| 0.x          | no  |

## Reporting a vulnerability

Please **do not** open public issues for security problems.

Use GitHub's [private security advisory flow](https://github.com/nkane/chippy/security/advisories/new) — it lets us discuss the issue and prepare a fix without disclosing the vulnerability prematurely.

If you cannot use the GitHub flow, email `nkanedev@gmail.com` with:

- A description of the issue and the impact you observed.
- Reproduction steps (a sample ROM or DAP launch.json is ideal).
- The chippy version (`chippy --version` or commit SHA).
- The platform (macOS / Linux / Windows; arch).

We aim to acknowledge reports within 72 hours and ship a patch within 30 days of acknowledgement. Once a fix is released we'll credit the reporter (unless you prefer to stay anonymous) in the release notes.

## Hardening guarantees we ship

Each tagged chippy release ships with:

- **Signed artifacts.** Every binary tarball and `checksums.txt` is signed with [cosign](https://docs.sigstore.dev/cosign/overview/) using GitHub's keyless OIDC identity. Verify a download:

  ```sh
  cosign verify-blob \
    --certificate-identity=https://github.com/nkane/chippy/.github/workflows/release.yml@refs/tags/<TAG> \
    --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
    --bundle chippy_<VERSION>_<OS>_<ARCH>.tar.gz.cosign.bundle \
    chippy_<VERSION>_<OS>_<ARCH>.tar.gz
  ```

- **SBOM.** Every archive ships a sibling `*.spdx.sbom.json` produced by syft so downstream packagers can audit dependencies.

- **Reproducible builds.** All Go builds use `-trimpath` (no absolute paths in the binary) and `-buildvcs=true` (commit + dirty bit stamped into `debug.BuildInfo`). Verify the embedded provenance with `go version -m chippy`.

- **CVE scanning in CI.** Every push runs `govulncheck ./...`; a fresh CVE in the dep graph fails CI before it can ship.

- **Automated dependency updates.** Dependabot tracks Go modules and GitHub Actions with weekly grouped PRs.

## What's out of scope

chippy is a debugger for 6502 ROMs you supply. We do not treat the following as security bugs:

- A malicious 6502 ROM that the emulator faithfully executes — chippy's job is to model the CPU, not to sandbox arbitrary 6502 code beyond what the hardware itself does.
- Crashes from intentionally malformed `.dbg` / `.bin` / `.prg` / `.hex` inputs that the loader rejects with a clean error.
- DoS via deeply pathological inputs (e.g. a 4 GB `.hex` file) — chippy is a developer tool, not a public service.

If you're unsure whether something qualifies, file the advisory anyway and we'll triage.
