//go:build nessy

package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// cpuCyclesPerFrame is the integer floor of the NES 2A03's master clock
// (1.789773 MHz) divided by the 60 Hz frame cadence: ~29830 cycles per
// frame. Ebiten's Update fires at 60 TPS by default, so running this
// many cycles per Update keeps the simulated wall-clock locked to
// real-time. The fractional remainder accumulates as a tiny drift; the
// PPU's vblank-at-scanline-241 timing handles the per-frame end-of-
// visible boundary so the user-visible effect is zero.
const cpuCyclesPerFrame = 29830

// keyMap is the standard NES controller binding nessy ships with.
// Configurable maps are a future polish item — for v0.1 the defaults
// match emulator convention (FCEUX / Nestopia).
var keyMap = []struct {
	key ebiten.Key
	btn joypad.Button
}{
	{ebiten.KeyArrowUp, joypad.ButtonUp},
	{ebiten.KeyArrowDown, joypad.ButtonDown},
	{ebiten.KeyArrowLeft, joypad.ButtonLeft},
	{ebiten.KeyArrowRight, joypad.ButtonRight},
	{ebiten.KeyZ, joypad.ButtonA},
	{ebiten.KeyX, joypad.ButtonB},
	{ebiten.KeyEnter, joypad.ButtonStart},
	{ebiten.KeyShiftRight, joypad.ButtonSelect},
}

// game implements ebiten.Game. It owns the NES bus and a CPU mutex
// shared with the DAP listener; every per-frame step takes the mutex so
// a concurrent DAP request can't observe a mid-instruction register
// snapshot.
type game struct {
	bus       *nesBus
	cpuMu     *sync.Mutex
	audio     *audioSink // optional; nil when -mute set or audio init failed
	titleBase string     // window title prefix; FPS appended every ~0.5 s
	frameNum  uint64
}

func newGame(bus *nesBus, cpuMu *sync.Mutex, titleBase string) *game {
	return &game{bus: bus, cpuMu: cpuMu, titleBase: titleBase}
}

// loopSteppedBanner is a one-shot diagnostic: prints to stderr the
// first time the game loop actually advances the CPU (gates released).
// Catches "gate races" — if this prints with PC=$C000 right after a
// DAP attach, the wait gate worked. If it prints earlier with no
// "debugger attached" line printed, the gate failed to engage.
var loopSteppedBanner atomic.Bool

func (g *game) Update() error {
	g.pollInput()
	// Gate the CPU stepping on two flags:
	//   1. waitForAttach — at-boot pause when nessy was launched
	//      under `chippy -nessy …` (-wait-for-debugger). Cleared on
	//      first DAP attach.
	//   2. dapAttached    — one or more DAP clients currently
	//      attached; the server's `continue` runLoop is the sole
	//      stepper, the game loop yields.
	// Draw still fires every frame so the screen reflects whatever
	// framebuffer state the PPU has rendered up to now.
	if waitForAttach.Load() || dapAttached.Load() > 0 {
		return nil
	}
	if !loopSteppedBanner.Swap(true) {
		fmt.Fprintf(os.Stderr,
			"nessy: game loop entering autonomous-step mode at PC=$%04X cycles=%d (waitForAttach=%v dapAttached=%d)\n",
			g.bus.cpu.PC, g.bus.cpu.Cycles, waitForAttach.Load(), dapAttached.Load())
	}
	g.cpuMu.Lock()
	target := g.bus.cpu.Cycles + cpuCyclesPerFrame
	for g.bus.cpu.Cycles < target && !g.bus.cpu.Halted {
		g.bus.cpu.Step()
	}
	// Drain APU samples while we hold cpuMu, then push them to the
	// audio sink (which has its own queue lock). Decouples the
	// audio thread from cpuMu — the pre-decoupling design had Read
	// blocking on cpuMu and burned ~38% of runtime in
	// pthread_cond_signal contention.
	mono := g.bus.apu.Samples()
	g.cpuMu.Unlock()
	g.audio.push(mono)
	// Refresh the window title with the actual TPS / FPS every ~0.5 s
	// (30 frames at 60 TPS). Cheap surface for the "is Update running
	// at 60Hz?" question.
	g.frameNum++
	if g.titleBase != "" && g.frameNum%30 == 0 {
		ebiten.SetWindowTitle(fmt.Sprintf("%s — TPS %.1f FPS %.1f",
			g.titleBase, ebiten.ActualTPS(), ebiten.ActualFPS()))
	}
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	screen.WritePixels(g.bus.ppu.FrameBuffer())
}

// Layout returns the NES's native resolution; Ebiten scales it to the
// requested window size set in main(). Keeps pixel-perfect integer
// scaling without us having to draw scaled images by hand.
func (g *game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return 256, 240
}

func (g *game) pollInput() {
	for _, m := range keyMap {
		g.bus.joy.P1.Set(m.btn, ebiten.IsKeyPressed(m.key))
	}
}
