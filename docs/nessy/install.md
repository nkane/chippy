# Install

## Pre-built binaries

Each `nessy-vX.Y.Z` tag attaches per-OS archives to its GitHub release:

[Latest release →](https://github.com/nkane/chippy/releases?q=nessy)

| Platform | Archive | Includes |
|---|---|---|
| macOS arm64 | `nessy_<ver>_darwin_arm64.tar.gz` | `nessy` binary + every homebrew demo ROM |
| macOS amd64 | `nessy_<ver>_darwin_amd64.tar.gz` | same |
| Linux amd64 | `nessy_<ver>_linux_amd64.tar.gz` | same |
| Windows amd64 | `nessy_<ver>_windows_amd64.zip` | same |

## Build from source

nessy ships behind a `nessy` build tag because Ebiten requires X11 / GL dev headers on Linux that the default CI runners don't carry. The `!nessy` stub in `cmd/nessy/main_stub.go` prints build instructions so `go build ./...` stays green for the rest of the repo.

### macOS

```sh
go build -tags=nessy -o nessy ./cmd/nessy
./nessy game.nes
```

### Linux (Debian / Ubuntu)

```sh
sudo apt-get install -y libgl1-mesa-dev xorg-dev libasound2-dev \
  libxcursor-dev libxinerama-dev libxi-dev libxrandr-dev
go build -tags=nessy -o nessy ./cmd/nessy
```

### Windows

```sh
go build -tags=nessy -o nessy.exe ./cmd/nessy
nessy.exe game.nes
```

## Browser

[Play in the browser](https://nkane.dev/chippy/playground/nessy/) — no install, ROM stays local.

## Optional flags

| Flag | Purpose |
|---|---|
| `-rom PATH` | iNES ROM (also accepts positional `nessy game.nes`) |
| `-dbg PATH` | ca65 / ld65 `.dbg` symbol file (auto-detected as `<rom>.dbg`) |
| `-dap-port N` | DAP listener port (default 14785; 0 disables) |
| `-scale N` | Window scale (default 3 → 768×720) |
| `-mute` | Disable audio output |
| `-wait-for-debugger` | Pause at boot until a DAP client attaches |
| `-pprof FILE` | Write a CPU profile for the lifetime of the run |
| `-oam-trace` | Print visible OAM each frame |
| `-frame-dump-every N` | PNG dump every N frames to `~/.nessy/dumps/` |

## Player hotkeys

| Key | Action |
|---|---|
| Arrows | D-pad |
| Z / X | A / B |
| Enter | Start |
| Right Shift | Select |
| Tab (held) | 4× fast-forward |
| F1–F4 | Save state into slots 1–4 |
| F5–F8 | Load saved state from matching slot |
| F11 | Toggle fullscreen |
| F12 | Save screenshot to `~/.nessy/screenshots/` |

## Configuration files

| File | Purpose |
|---|---|
| `~/.nessy/recent` | Recent ROM list (5 newest). `nessy N` opens slot N. |
| `~/.nessy/controller.json` | Remap any NES button to any Ebiten key. |
| `~/.nessy/sav/<rom-hash>.sav` | Battery-backed PRG-RAM persistence (auto). |
| `~/.nessy/states/<rom-hash>-slot<N>.state` | Save states. |
| `~/.nessy/screenshots/<rom>-<timestamp>.png` | F12 captures. |
| `~/.nessy/dumps/F<N>.png` | `-frame-dump-every` output. |
