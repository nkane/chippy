//go:build nessy

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nkane/chippy/internal/nes"
	"github.com/nkane/chippy/internal/symbols"
)

func main() {
	var (
		romPath   = flag.String("rom", "", "iNES ROM path (positional arg also accepted)")
		dbgPath   = flag.String("dbg", "", "cc65/ld65 .dbg symbol file (auto-detected as <rom>.dbg if omitted)")
		dapPort   = flag.Int("dap-port", 14785, "DAP server TCP port; 0 disables the listener")
		scale     = flag.Int("scale", 3, "integer window scale (3 → 768x720)")
		mute      = flag.Bool("mute", false, "disable audio output (APU still runs; samples are dropped)")
		waitDbg   = flag.Bool("wait-for-debugger", false, "pause the CPU at boot until a DAP client attaches (set by `chippy -nessy`)")
		pprofPath = flag.String("pprof", "", "write a CPU profile to FILE for the lifetime of the run; analyze with `go tool pprof FILE`")
		frameDump = flag.Int("frame-dump-every", 0, "dump the framebuffer as PNG to ~/.nessy/dumps/F<N>.png every N frames (0 = off); expensive — diagnostic only")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: nessy [-rom PATH | PATH] [-dbg PATH] [-dap-port N] [-scale N] [-mute] [-wait-for-debugger] [-pprof FILE]\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// CPU profile (optional). Starts before any heavy work so the
	// Ebiten game loop's per-frame Update + Draw show up in the
	// sample stream. Stopped via deferred close — quitting the
	// nessy window or hitting Ctrl+C from the launching terminal
	// flushes the file.
	if *pprofPath != "" {
		f, err := os.Create(*pprofPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nessy: pprof create:", err)
			os.Exit(1)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintln(os.Stderr, "nessy: pprof start:", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "nessy: CPU profile recording to", *pprofPath)
		defer func() {
			pprof.StopCPUProfile()
			if err := f.Close(); err != nil {
				fmt.Fprintln(os.Stderr, "nessy: pprof close:", err)
			}
			fmt.Fprintln(os.Stderr, "nessy: profile written. Analyze with `go tool pprof", *pprofPath+"`")
		}()
	}

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

	romBytes, err := os.ReadFile(*romPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy:", err)
		os.Exit(1)
	}
	rom, err := nes.ParseBytes(romBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: parse ROM:", err)
		os.Exit(1)
	}

	bus, err := buildNES(rom)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: build NES:", err)
		os.Exit(1)
	}

	// Battery-backed PRG-RAM (#267). Load any existing .sav into
	// the cart's PRG-RAM before the CPU runs; write it back when
	// the game loop exits. SHA-keyed so renaming the ROM doesn't
	// orphan the save.
	loadBattery(bus.cart, romBytes)
	defer saveBattery(bus.cart, romBytes)

	// Optional ca65 / ld65 .dbg symbol + source map. Auto-detect as
	// `<rom>.dbg` sibling if the user didn't pass -dbg. Missing /
	// unreadable files surface as warnings, not errors — running
	// without source info is fine.
	dbg := *dbgPath
	if dbg == "" {
		dbg = symbols.SiblingDbg(*romPath)
	}
	var (
		syms   *symbols.Table
		srcMap *symbols.SourceMap
	)
	if dbg != "" {
		if t, err := symbols.LoadDbg(dbg); err != nil {
			fmt.Fprintln(os.Stderr, "nessy: load dbg:", err)
		} else {
			syms = t
		}
		if sm, err := symbols.LoadSourceMap(dbg); err != nil {
			fmt.Fprintln(os.Stderr, "nessy: load source map:", err)
		} else {
			srcMap = sm
		}
	}

	// If launched under `chippy -nessy …`, the user wants to attach
	// before the game runs. Set the gate flag BEFORE starting the
	// DAP listener so we can't race against an instant client
	// connection.
	if *waitDbg {
		if *dapPort <= 0 {
			fmt.Fprintln(os.Stderr, "nessy: -wait-for-debugger requires -dap-port > 0")
			os.Exit(2)
		}
		waitForAttach.Store(true)
		fmt.Fprintln(os.Stderr, "nessy: -wait-for-debugger active — CPU paused at boot until a DAP client attaches")
	}

	// CPUMu serializes the game loop's per-frame stepping with any DAP
	// requests that arrive concurrently. Same pattern chippy's `:dap`
	// command uses.
	cpuMu := &sync.Mutex{}
	if *dapPort > 0 {
		go runDAPListener(*dapPort, bus, cpuMu, syms, srcMap)
		fmt.Fprintf(os.Stderr, "nessy: DAP listener on :%d (attach with `chippy -dap-attach tcp:localhost:%d`)\n",
			*dapPort, *dapPort)
	}

	titleBase := fmt.Sprintf("nessy — %s", filepath.Base(*romPath))
	g := newGame(bus, cpuMu, titleBase)
	g.frameDumpEvery = *frameDump
	sink, err := newAudioSink(*mute)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: audio init failed (continuing muted):", err)
	}
	g.audio = sink
	sink.start()
	defer sink.close()
	ebiten.SetWindowSize(256*(*scale), 240*(*scale))
	ebiten.SetWindowTitle(titleBase)
	ebiten.SetTPS(60)
	if err := ebiten.RunGame(g); err != nil {
		fmt.Fprintln(os.Stderr, "nessy:", err)
		os.Exit(1)
	}
}
