package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/loader"
	"github.com/nkane/chippy/internal/symbols"
	"github.com/nkane/chippy/internal/tui"
)

func main() {
	var (
		romPath  = flag.String("rom", "", "program to load (.bin .prg .hex .o)")
		loadAddr = flag.Uint("addr", 0x8000, "load address for raw .bin (ignored for .prg/.hex/.o)")
		resetVec = flag.Uint("reset", 0, "reset vector override (0 = use file's existing vector or load address)")
		cfg      = flag.String("cfg", "", "ld65 linker config (.cfg) — required when loading .o files")
		dbgPath  = flag.String("dbg", "", "cc65 .dbg symbol file (auto-detected as <rom>.dbg if omitted)")
	)
	flag.Parse()

	ram := cpu.NewRAM()
	var loaded *loader.Result

	if *romPath != "" {
		var err error
		loaded, err = loader.Load(ram, *romPath, loader.Options{
			Addr:      uint16(*loadAddr),
			LinkerCfg: *cfg,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "load:", err)
			os.Exit(1)
		}
	} else {
		// Built-in demo at $8000.
		demo := []byte{
			0xA9, 0x00, // LDA #$00
			0xAA,       // TAX
			0xE8,       // INX
			0x8A,       // TXA
			0x69, 0x01, // ADC #$01
			0x4C, 0x02, 0x80, // JMP $8002
		}
		ram.Load(0x8000, demo)
		loaded = &loader.Result{LoadAddr: 0x8000, Size: len(demo), Format: "demo"}
	}

	// Reset vector resolution:
	//   1. -reset flag wins
	//   2. otherwise leave whatever the binary already wrote into $FFFC/D
	//   3. if those bytes are still zero (untouched), fall back to load addr
	switch {
	case *resetVec != 0:
		ram.Write(cpu.VecReset, byte(*resetVec))
		ram.Write(cpu.VecReset+1, byte(*resetVec>>8))
	case ram.Read(cpu.VecReset) == 0 && ram.Read(cpu.VecReset+1) == 0:
		ram.Write(cpu.VecReset, byte(loaded.LoadAddr))
		ram.Write(cpu.VecReset+1, byte(loaded.LoadAddr>>8))
	}

	// Symbol table: explicit -dbg, sibling auto-detect, or ld65-produced .dbg.
	var syms *symbols.Table
	dbg := *dbgPath
	if dbg == "" {
		if loaded.LinkedBin != "" {
			dbg = symbols.SiblingDbg(loaded.LinkedBin)
		}
		if dbg == "" && *romPath != "" {
			dbg = symbols.SiblingDbg(*romPath)
		}
	}
	if dbg != "" {
		t, err := symbols.LoadDbg(dbg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: load dbg:", err)
		} else {
			syms = t
		}
	}

	// Source map (PC -> file:line) from same .dbg file.
	var srcMap *symbols.SourceMap
	if dbg != "" {
		sm, err := symbols.LoadSourceMap(dbg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: load source map:", err)
		} else {
			srcMap = sm
		}
	}

	c := cpu.New(ram)

	// Wrap the bus with WBus so memory watchpoints can intercept reads/writes.
	// We construct CPU on raw RAM first (to keep cpu.New's signature simple),
	// then swap c.Bus to the wrapper. WithWBus attaches the CPU pointer and
	// hands the watch map to the wrapper.
	wbus := tui.NewWBus(ram)
	c.Bus = wbus

	model := tui.New(c, ram).WithWBus(wbus)
	if syms != nil {
		model = model.WithSymbols(syms)
	}
	if srcMap != nil {
		model = model.WithSourceMap(srcMap)
	}
	if *romPath != "" {
		model = model.WithStatePath(tui.DefaultStatePath(*romPath))
	}

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
