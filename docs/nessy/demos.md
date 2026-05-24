# Homebrew demos

Every demo under `roms/demos/` is hand-rolled ca65 + ld65 source with a checked-in `.nes` artifact. Each doubles as a regression test under `cmd/nessy/demo_*_test.go` — either a SHA-pinned framebuffer hash (visual demos) or a sample-presence assertion (audio demos).

Toolchain: `brew install cc65` (macOS) or `sudo apt-get install cc65` (Debian / Ubuntu). Rebuild via `make -C roms/demos all`. Toolchain isn't required to run — the `.nes` files are committed.

| Demo | What it tests | Source |
|---|---|---|
| [`hello-bg`](https://github.com/nkane/chippy/tree/main/roms/demos/hello-bg) | PPU bg renderer + palette + nametable + reset path | Static "HELLO NESSY" title screen |
| [`input-echo`](https://github.com/nkane/chippy/tree/main/roms/demos/input-echo) | `$4016` joypad strobe + serial shift + per-frame VRAM writes | 8 indicator boxes light up under live joypad input |
| [`vblank-bounce`](https://github.com/nkane/chippy/tree/main/roms/demos/vblank-bounce) | PPU NMI line + CPU NMI service + `JMP self` idle | Single tile bounces inside the playfield |
| [`triangle-arpeggio`](https://github.com/nkane/chippy/tree/main/roms/demos/triangle-arpeggio) | APU triangle channel + NMI-driven note rotation | A-major arpeggio (audio only) |
| [`noise-drum`](https://github.com/nkane/chippy/tree/main/roms/demos/noise-drum) | APU noise channel + LFSR feedback | Low/high noise drum (audio only) |
| [`all-channels`](https://github.com/nkane/chippy/tree/main/roms/demos/all-channels) | Non-linear DAC mixer under multi-channel load | Pulse + triangle + noise chord (audio only) |
| [`dmc-sample`](https://github.com/nkane/chippy/tree/main/roms/demos/dmc-sample) | DMC channel DMA fetch + delta-PCM + loop bit | 65-byte alternating-bit sample looped |
| [`mmc1-banks`](https://github.com/nkane/chippy/tree/main/roms/demos/mmc1-banks) | MMC1 serial-shift PRG bank switching | BG flashes between two colours twice per second |

## Run

```sh
./nessy roms/demos/hello-bg/hello-bg.nes
./nessy roms/demos/all-channels/all-channels.nes
```

## Inspect headless

Every demo test ships with an `_Inspect` variant that prints either a textual screenshot or per-frame state. Useful when you can't open a display:

```sh
CHIPPY_DEMO_INSPECT=1 go test -tags=nessy -run TestDemo_HelloBG_Inspect ./cmd/nessy/... -v
```

The build-tag gate keeps the test out of CI's general suite where Ebiten dev headers aren't installed.
