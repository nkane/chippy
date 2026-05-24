# nessy

nessy is an NES emulator built on top of chippy's 6502 CPU + bus infrastructure. The same `cpu.CPU` that runs ca65 demos under the chippy TUI also drives the Ricoh 2A03 inside an Ebiten window, with the chippy DAP server attached so you can pause + step + breakpoint a live NES ROM from any DAP-speaking editor.

```
chippy -nessy game.nes
```

That single command spawns nessy + dials its DAP listener + opens the TUI in attach mode paused at the reset vector. No two-terminal dance.

## Status

| Version | Highlight |
|---|---|
| v0.1 | NROM + DAP attach + first homebrew demos |
| v0.2 | Sprites, scrolling, OAMDMA — SMB1 boots |
| v0.3 | Full audio (5 channels + non-linear DAC mixer), MMC1 |
| v0.4 | UxROM / CNROM / MMC3, save states, PPU per-dot accuracy, battery PRG-RAM |
| v0.5 | VRC2 / VRC4 / FME-7 mappers, cycle-accuracy hardening, player UX |
| v0.6 | VRC6 + audio, VRC7 cart, Sunsoft 5B audio, WASM playground, DMC/OAMDMA contention |

## Mapper coverage

NROM, MMC1, UxROM, CNROM, MMC3 (Sharp + NEC RevA), VRC2, VRC4, VRC6 + audio, VRC7 (cart only; OPLL FM synth in v0.7), FME-7 + Sunsoft 5B audio.

Headliners that now play: Super Mario Bros, Zelda 1, Final Fantasy, Metroid, Castlevania II + III JP, Mega Man 1-6, SMB3, Crisis Force, Gimmick!, Lagrange Point (silent).

## In your browser

[Try nessy in the browser](https://nkane.dev/chippy/playground/nessy/) — Ebiten js/wasm build with a default demo + drag-drop loader for your own ROMs.

## What's next

- [v0.6 epic](https://github.com/nkane/chippy/issues/305)
- [v0.7 OPLL FM synth](https://github.com/nkane/chippy/issues/315) — Lagrange Point's soundtrack.

See [`install`](install.md) for binaries + build instructions, [`demos`](demos.md) for the homebrew ROM suite, and [`debugging`](debugging.md) for the DAP-attach workflow.
