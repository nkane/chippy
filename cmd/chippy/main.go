package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/loader"
	"github.com/nkane/chippy/internal/peripheral"
	"github.com/nkane/chippy/internal/symbols"
	"github.com/nkane/chippy/internal/tui"
)

func main() {
	var (
		romPath    = flag.String("rom", "", "program to load (.bin .prg .hex .o)")
		loadAddr   = flag.Uint("addr", 0x8000, "load address for raw .bin (ignored for .prg/.hex/.o)")
		resetVec   = flag.Uint("reset", 0, "reset vector override (0 = use file's existing vector or load address)")
		cfg        = flag.String("cfg", "", "ld65 linker config (.cfg) — required when loading .o files")
		dbgPath    = flag.String("dbg", "", "cc65 .dbg symbol file (auto-detected as <rom>.dbg if omitted)")
		cpuFlag    = flag.String("cpu", "nmos", "CPU variant: nmos | 65c02")
		tracePath  = flag.String("trace", "", "write per-instruction execution trace to this file")
		runOnStart = flag.Bool("run-on-start", false, "start the CPU running instead of paused (pair with -trace for non-interactive capture)")
		dapMode    = flag.String("dap", "", "run as a Debug Adapter Protocol server instead of the TUI: 'stdio' or 'tcp:PORT'")
	)
	flag.Parse()

	if *dapMode != "" {
		runDAP(*dapMode)
		return
	}

	variant := cpu.VariantNMOS
	switch strings.ToLower(*cpuFlag) {
	case "nmos", "6502":
		variant = cpu.VariantNMOS
	case "65c02", "cmos", "cmos65c02":
		variant = cpu.VariantCMOS65C02
	default:
		fmt.Fprintf(os.Stderr, "unknown -cpu value %q (want nmos or 65c02)\n", *cpuFlag)
		os.Exit(2)
	}

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

	// MMIO bus sits between WBus and RAM. Peripherals registered here
	// intercept reads/writes to their claimed regions; everything else
	// falls through to RAM. Note: the loader and the reset-vector helpers
	// above write directly to `ram`, deliberately bypassing MMIO, so a
	// program loaded at $F001 would land in RAM and never reach the
	// peripheral — peripherals live in addresses no ROM should occupy.
	mmio := cpu.NewMMIO(ram)
	textOut := peripheral.NewTextOutput(0xF001)
	keyIn := peripheral.NewKeyboardInput(0xF004, 0xF005)
	if err := mmio.Register(textOut); err != nil {
		fmt.Fprintln(os.Stderr, "register text output:", err)
		os.Exit(1)
	}
	if err := mmio.Register(keyIn); err != nil {
		fmt.Fprintln(os.Stderr, "register keyboard:", err)
		os.Exit(1)
	}

	c := cpu.NewVariant(mmio, variant)

	tracer := cpu.NewFileTracer()
	c.Tracer = tracer
	defer func() { _ = tracer.Close() }()
	if *tracePath != "" {
		if err := tracer.SetPath(*tracePath); err != nil {
			fmt.Fprintln(os.Stderr, "trace:", err)
			os.Exit(1)
		}
		tracer.Enable()
	}

	// Wrap the bus with WBus so memory watchpoints can intercept reads/writes.
	// We construct CPU on MMIO first (to keep cpu.New's signature simple),
	// then swap c.Bus to the wrapper. WithWBus attaches the CPU pointer and
	// hands the watch map to the wrapper.
	wbus := tui.NewWBus(mmio)
	c.Bus = wbus

	model := tui.New(c, ram).
		WithWBus(wbus).
		WithTextOutput(textOut).
		WithKeyboard(keyIn).
		WithTracer(tracer).
		WithHistoryPath(tui.DefaultHistoryPath()).
		WithRunOnStart(*runOnStart)
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
