# hello-bg

The first homemade nessy demo. Renders a static "HELLO NESSY" string centered on the nametable. No animation, no input, no scrolling — exercises the iNES → cart → MMIO → CPU + PPU integration end-to-end with a real hand-rolled ROM.

## What it tests

| Subsystem | How |
|---|---|
| PPU palette RAM | $3F00 universal bg + 3 sub-palette entries set via $2006 / $2007 |
| PPU nametable + attribute table | Cleared via 1024-byte loop, then "HELLO NESSY" written at offset $21CA |
| PPU pattern table 0 | Inline CHR data (8 KiB) ships glyphs for H, E, L, O, N, S, Y at their ASCII tile indices |
| PPU rendering | `renderFrame` at vblank entry consumes nametable + attribute + pattern + palette |
| CPU integration | 2 vblank-wait spin loops; full reset routine including RAM clear |
| Reset vector wiring | `$FFFC` points at `reset:` in CODE segment |
| Cart `$8000-$FFFF` claim | All PRG access flows through the NROM CPU-side wrapper |

## Build

```sh
make -C roms/demos hello-bg
```

The built `hello-bg.nes` is committed so toolchain installation isn't required to run or test the demo.

## Run

```sh
go build -tags=nessy -o nessy ./cmd/nessy
./nessy roms/demos/hello-bg/hello-bg.nes
```

Expected: blue background, near-white "HELLO NESSY" text centered on screen.

## Regression test

`cmd/nessy/demo_test.go::TestDemo_HelloBG` boots the ROM headlessly, advances 5 frames, hashes the framebuffer, and compares to a pinned SHA-256 (`helloBGFrameSHA`). Any change in PPU output, palette mapping, or nametable layout shifts the hash and trips the test. Update procedure:

1. Modify the demo source / rebuild.
2. Run `go test -run TestDemo_HelloBG ./cmd/nessy/... -v` — note the "got" hash.
3. `CHIPPY_DEMO_INSPECT=1 go test -run TestDemo_HelloBG_Inspect -v ./cmd/nessy/...` to render a textual screenshot and confirm output looks right.
4. Update `helloBGFrameSHA` to the new value.

## License

MIT (same as chippy). Original work — safe to redistribute as part of the chippy source tree.
