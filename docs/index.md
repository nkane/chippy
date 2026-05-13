# chippy

TUI 6502 emulator + ca65/cc65 source-level debugger, written in Go.

[![ci](https://github.com/nkane/chippy/actions/workflows/ci.yml/badge.svg)](https://github.com/nkane/chippy/actions/workflows/ci.yml)
[![release](https://img.shields.io/github/v/release/nkane/chippy?sort=semver)](https://github.com/nkane/chippy/releases)

## Try it in your browser

The [**playground**](/chippy/playground/) runs the same emulator core
this repo ships, compiled to WebAssembly. Pick a canned demo (or drop
in your own `.bin` / `.prg` / `.hex`) and step through it — no install
required.

## Install on your machine

| Platform | Install |
|---|---|
| macOS / Linux (Homebrew) | `brew tap nkane/tap && brew install chippy` |
| Debian / Ubuntu | `sudo dpkg -i chippy_*_linux_amd64.deb` |
| Fedora / RHEL | `sudo rpm -i chippy_*_linux_amd64.rpm` |
| Alpine | `sudo apk add --allow-untrusted chippy_*_linux_amd64.apk` |
| Arch Linux (AUR) | `yay -S chippy-bin` |
| Windows / others | [Releases page](https://github.com/nkane/chippy/releases) |
| From source | `go install github.com/nkane/chippy/cmd/chippy@latest` |

Every release artifact ships with a `cosign` keyless OIDC signature
plus an SPDX SBOM — see [SECURITY.md](https://github.com/nkane/chippy/blob/main/SECURITY.md)
for the verification recipe.

## Why chippy

Plenty of 6502 emulators exist. chippy's pitch is **debugger-first**:

- **TUI + DAP + WASM, one engine.** Same NMOS / 65C02 core powers
  the terminal UI, the [DAP server](dap.md) (VS Code / nvim-dap /
  JetBrains can drive it), and the in-browser playground.
- **Source-level debugging from C and ca65.** Auto-detected `.dbg`
  files turn `.bin` addresses into `file:line` and symbol names —
  breakpoints, watches, and conditional expressions resolve against
  the same names you wrote.
- **Reverse-step that scales.** Page-level copy-on-write snapshots
  cost ~hundreds of bytes per step, so the rewind ring works during
  free-run too (a 1000-iter tight loop fits in <1 MiB).
- **Real 6502 + 65C02 compliance.** Klaus Dormann's functional tests
  pass end-to-end for both variants; an exhaustive BCD sweep covers
  every ADC / SBC input combination.

## Where to next

- [Quick start](quickstart.md) — your first ROM in 5 minutes.
- [DAP server reference](dap.md) — editor integration.
- [Architecture dump](context.md) — internals + project history.
- [Contributing](https://github.com/nkane/chippy/blob/main/CONTRIBUTING.md)
- [Editor matrix](editors.md) — which editors are tested.
