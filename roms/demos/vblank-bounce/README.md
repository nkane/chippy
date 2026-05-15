# vblank-bounce

nessy's third homemade demo. A single 8×8 tile bounces inside the playfield, position updated by an NMI handler each frame.

## What it tests

| Subsystem | How |
|---|---|
| PPU NMI line | PPUCTRL bit 7 enabled; vblank @ scanline 241 raises NMI |
| CPU NMI service | Push P / push PC / vector via $FFFA / RTI |
| Per-NMI nametable updates | Every NMI: erase, advance pos with bounce, draw, scroll-reset |
| Vblank window discipline | All $2006/$2007 writes happen inside the NMI handler, never during active rendering |
| `JMP self` idle pattern | Main loop spins; all work in NMI. Verifies the CPU's variant-gated halt heuristic doesn't trip on NES |

## How it works

State (zero page):
- `pos_x`, `pos_y` — current cell, range `[2, 29]` × `[2, 27]`.
- `dir_x`, `dir_y` — `$01` or `$FF` (two's complement -1).

Each NMI:
1. Erase tile at current `(pos_x, pos_y)`.
2. Advance `pos_x += dir_x`; if `pos_x >= 30` or `< 2`, clamp + flip `dir_x`.
3. Same for y with bounds `[2, 27]`.
4. Draw tile at new `(pos_x, pos_y)`.
5. Restore scroll (mandatory after $2006 writes).
6. RTI.

## Build

```sh
make -C roms/demos vblank-bounce
```

## Run

```sh
go build -tags=nessy -o nessy ./cmd/nessy
./nessy roms/demos/vblank-bounce/vblank-bounce.nes
```

Watch the tile bounce diagonally across the screen. With no input, the trajectory is deterministic — same start, same path, same bounce pattern every run.

## Regression tests

`cmd/nessy/demo_vblank_bounce_test.go` pins two framebuffer SHAs at distinct frame counts:

| Test | Frames | Expected |
|---|---|---|
| `TestDemo_VBlankBounce_Early` | 5 | Tile near starting position |
| `TestDemo_VBlankBounce_Late` | 30 | Tile somewhere along the bounce path |

The two SHAs must differ — test enforces that — so a frozen NMI line or a broken erase path trips it.

Inspect at any frame count:

```sh
CHIPPY_DEMO_INSPECT=1 CHIPPY_DEMO_FRAMES=15 \
  go test -run TestDemo_VBlankBounce_Inspect -v ./cmd/nessy/...
```

## License

MIT (same as chippy). Original work.
