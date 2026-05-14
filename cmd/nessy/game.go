//go:build nessy

package main

import (
	"sync"

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
	bus   *nesBus
	cpuMu *sync.Mutex
}

func newGame(bus *nesBus, cpuMu *sync.Mutex) *game {
	return &game{bus: bus, cpuMu: cpuMu}
}

func (g *game) Update() error {
	g.pollInput()
	g.cpuMu.Lock()
	defer g.cpuMu.Unlock()
	target := g.bus.cpu.Cycles + cpuCyclesPerFrame
	for g.bus.cpu.Cycles < target && !g.bus.cpu.Halted {
		g.bus.cpu.Step()
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
