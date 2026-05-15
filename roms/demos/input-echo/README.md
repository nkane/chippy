# input-echo

nessy's second homemade demo. Eight indicator boxes arranged in a controller layout flip between empty and full as you hold buttons.

## Layout

```
        [Up]

[Left]      [Right]    [Select][Start]    [A][B]

       [Down]
```

Each box is a single 8×8 background tile. The Up / Down / Left / Right cluster sits on the left; Select / Start / A / B on the right. Holding a button replaces its outline tile (`$30`) with a solid block (`$31`); release returns it.

## What it tests

| Subsystem | How |
|---|---|
| Joypad ($4016 strobe + serial shift) | Every frame: write 1 then 0 to $4016, read 8 sequential `lda $4016` to drain A/B/Sel/Sta/U/D/L/R |
| Per-frame VRAM update | Each frame after vblank, write 8 nametable cells via $2006/$2007 |
| Vblank discipline | All VRAM writes occur inside the vblank window so the renderer never sees a partial frame |
| Scroll-reset after $2006 | Two writes to $2005 restore (0,0) after every PPUADDR clobber |
| CPU integration | Same reset routine as hello-bg + a tight main loop with conditional indirect writes |

## Build

```sh
make -C roms/demos input-echo
```

## Run

```sh
go build -tags=nessy -o nessy ./cmd/nessy
./nessy roms/demos/input-echo/input-echo.nes
```

Controls (chippy's default mapping):

| Key | Button |
|---|---|
| Arrows | D-pad |
| Z | A |
| X | B |
| Enter | Start |
| Right Shift | Select |

## Regression tests

`cmd/nessy/demo_input_echo_test.go` runs the demo headlessly in two states:

| Test | Setup | Expected |
|---|---|---|
| `TestDemo_InputEcho_Idle` | No buttons pressed | All 8 indicators empty |
| `TestDemo_InputEcho_UpPressed` | `bus.joy.P1.Set(ButtonUp, true)` | Up indicator full, others empty |

Both pin SHA-256 hashes of the framebuffer. The two SHAs must differ — the test enforces that — so a broken joypad path immediately trips. Inspect what the framebuffer actually looks like with:

```sh
CHIPPY_DEMO_INSPECT=1 go test -run TestDemo_InputEcho_Inspect -v ./cmd/nessy/...
CHIPPY_DEMO_INSPECT=1 CHIPPY_DEMO_BUTTON=Up   go test -run TestDemo_InputEcho_Inspect -v ./cmd/nessy/...
CHIPPY_DEMO_INSPECT=1 CHIPPY_DEMO_BUTTON=A    go test -run TestDemo_InputEcho_Inspect -v ./cmd/nessy/...
```

## License

MIT (same as chippy). Original work.
