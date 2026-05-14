# nessy spike results

Recording the outcome of the design spikes called for in
[docs/plans/nessy.md](nessy.md) before the v0.1 epic starts piling
PRs.

## Spike #1 — `cpu.Step()` bus-ticker hook (#175)

**Status: PASS. Merged in PR #183.**

Required: per-instruction tick fan-out for the PPU + APU, with no-ticker
overhead < 5 ns/op so perfgate doesn't trip.

Result (Apple M5, `go test -bench=. -benchtime=2s`):

| benchmark                     | ns/op  | ceiling |
|-------------------------------|--------|---------|
| `BenchmarkStep_NMOS`          |  7.86  |  25.00  |
| `BenchmarkStep_CMOS`          |  8.30  |  25.00  |
| `BenchmarkStep_WithSnapshot`  | 61.78  | 200.00  |
| `BenchmarkStep_WithTicker`    |  8.73  | (new)   |

Ticker overhead: **+0.87 ns/op** when a Ticker is registered; **0 ns**
on the no-ticker fast path (cached `c.busTicker` nil-check). Perfgate
all-green.

Decision: ship. PPU + APU can register against `MMIO` as both a
`Peripheral` and a `Ticker`.

## Spike #2 — Ebiten as the graphics library

**Status: PASS for build matrix; runtime smoke pending user verification.**

Required:
1. Open a window, blit a 256x240 RGBA framebuffer scaled to a usable
   size, sustain 60 fps.
2. Tolerate a sibling goroutine doing NES-CPU-rate work (~1.79 MHz)
   without ticks-per-second dropping.
3. Build cleanly for both native and `js/wasm` so the playground
   option stays open.

Build matrix:

| target            | result                       | binary size |
|-------------------|------------------------------|-------------|
| `darwin/arm64`    | clean (cgo for CoreVideo)    | 9.4 MB      |
| `js/wasm`         | clean                        | 9.8 MB      |

cgo on darwin emits a deprecation warning about `CVDisplayLinkStart` in
macOS 15+. Pre-existing in Ebiten's macOS backend; tracked upstream;
non-fatal for v0.1.

The spike binary at `cmd/spike-ebiten/` does:
- 256x240 RGBA framebuffer painted with a moving gradient every
  Update() (touches every byte once — representative of PPU work).
- `WritePixels` into an `*ebiten.Image`, draw scaled 3x.
- Background goroutine burns ~29 830 atomic-add ops per 16.6 ms slice
  to mimic the 2A03's 1.789 MHz clock under load.
- Live HUD via window title: actual FPS, actual TPS, simulated MHz.

**To verify locally:**

```sh
go run -tags=spike ./cmd/spike-ebiten
```

The `spike` build tag keeps CI (Ubuntu runner without X11 dev
headers) from trying to compile this binary.

Watch the window title. Pass criteria:
- `fps >= 59`
- `tps >= 59`
- simulated MHz reading converges around 1.79 ± 0.05.

If pass: commit Ebiten as the v0.1 graphics library. If fail (FPS
< 55 or TPS-FPS divergence > 10): re-evaluate vs raylib-go (cgo,
SDL2-style) or SDL2 native binding.

## Open decisions deferred

- **WASM scope for nessy**: build works; do we ship it in v0.1 or
  v0.x? Plan says v0.x stretch; reaffirmed by spike — Ebiten's WASM
  path drags in 9.8 MB and the audio/controller stories differ
  enough to warrant a separate phase.
- **Audio mixing pipeline**: not exercised by this spike. Ebiten
  ships an audio package targeting 44.1 kHz mono / 48 kHz stereo;
  APU at 1.79 MHz needs a lowpass + decimation pass. Carry into
  v0.2 (APU) issue.

## Cleanup

`cmd/spike-ebiten/` is throwaway. Delete after the first real PPU
PR (issue #177) lands — its dependency on Ebiten replicates into
`cmd/nessy/` and there's no reason to keep two windowing binaries.
