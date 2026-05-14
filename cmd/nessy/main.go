//go:build nessy

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nkane/chippy/internal/nes"
)

func main() {
	var (
		romPath = flag.String("rom", "", "iNES ROM path (positional arg also accepted)")
		dapPort = flag.Int("dap-port", 14785, "DAP server TCP port; 0 disables the listener")
		scale   = flag.Int("scale", 3, "integer window scale (3 → 768x720)")
		mute    = flag.Bool("mute", false, "disable audio (no-op in v0.1, APU lands in v0.2)")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: nessy [-rom PATH | PATH] [-dap-port N] [-scale N] [-mute]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	_ = mute

	// Accept the positional form: `nessy game.nes`.
	if *romPath == "" && flag.NArg() == 1 {
		*romPath = flag.Arg(0)
	}
	if *romPath == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *scale < 1 {
		fmt.Fprintln(os.Stderr, "nessy: -scale must be >= 1")
		os.Exit(2)
	}

	f, err := os.Open(*romPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy:", err)
		os.Exit(1)
	}
	rom, err := nes.Parse(f)
	_ = f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: parse ROM:", err)
		os.Exit(1)
	}

	bus, err := buildNES(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: build NES:", err)
		os.Exit(1)
	}

	// CPUMu serializes the game loop's per-frame stepping with any DAP
	// requests that arrive concurrently. Same pattern chippy's `:dap`
	// command uses.
	cpuMu := &sync.Mutex{}
	if *dapPort > 0 {
		go runDAPListener(*dapPort, bus, cpuMu)
		fmt.Fprintf(os.Stderr, "nessy: DAP listener on :%d (attach with `chippy -dap-attach tcp:localhost:%d`)\n",
			*dapPort, *dapPort)
	}

	g := newGame(bus, cpuMu)
	ebiten.SetWindowSize(256*(*scale), 240*(*scale))
	ebiten.SetWindowTitle(fmt.Sprintf("nessy — %s", filepath.Base(*romPath)))
	ebiten.SetTPS(60)
	if err := ebiten.RunGame(g); err != nil {
		fmt.Fprintln(os.Stderr, "nessy:", err)
		os.Exit(1)
	}
}
