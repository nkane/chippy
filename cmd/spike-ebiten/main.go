//go:build spike

// cmd/spike-ebiten — graphics-library evaluation spike for nessy
// (issue #182). Validates that Ebiten can sustain 60 fps blitting a
// 256x240 RGBA framebuffer while a background goroutine simulates an
// NES CPU's cycle workload.
//
// Build-tagged `spike` so the default `go build ./...` (and CI Ubuntu
// runners that lack X11 dev headers) skip it. Run locally:
//
//	go run -tags=spike ./cmd/spike-ebiten
//
// Press ESC to quit. The window title shows the live frame timing
// and the simulated cycle throughput. Pass / fail decision criteria
// for the spike are documented in docs/plans/spike-results.md.
//
// This binary is throwaway — delete after the v0.1 PPU lands.
package main

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	nesWidth  = 256
	nesHeight = 240
	scale     = 3 // window is 768x720
)

// game implements ebiten.Game. The 256x240 RGBA framebuffer is
// repainted every Update() with a cheap animated pattern so the
// throughput numbers reflect a real blit + a real refresh.
type game struct {
	framebuf [nesWidth * nesHeight * 4]byte
	img      *ebiten.Image
	frame    uint64

	// cpuCycles counts simulated CPU cycles a background goroutine
	// burns. Approximates the 1.789773 MHz NES CPU clock.
	cpuCycles atomic.Uint64
}

func newGame() *game {
	g := &game{img: ebiten.NewImage(nesWidth, nesHeight)}
	go g.simulateCPU()
	return g
}

func (g *game) Update() error {
	if ebiten.IsKeyPressed(ebiten.KeyEscape) {
		return ebiten.Termination
	}
	g.frame++
	g.paintTestPattern()
	g.img.WritePixels(g.framebuf[:])
	return nil
}

func (g *game) Draw(screen *ebiten.Image) {
	opts := &ebiten.DrawImageOptions{}
	opts.GeoM.Scale(scale, scale)
	screen.DrawImage(g.img, opts)
	// Live HUD via window title — no overlay drawing so the framebuf
	// numbers reflect pure 256x240 blit cost.
	fps := ebiten.ActualFPS()
	tps := ebiten.ActualTPS()
	cyc := g.cpuCycles.Load()
	ebiten.SetWindowTitle(fmt.Sprintf(
		"spike-ebiten — %.1f fps / %.1f tps / sim %.2f MHz",
		fps, tps, float64(cyc)/1e6/float64(g.frame)*60.0))
}

func (g *game) Layout(w, h int) (int, int) { return nesWidth * scale, nesHeight * scale }

// paintTestPattern fills the framebuf with a moving gradient —
// representative of a PPU's per-pixel work without actually doing
// PPU work. Touches every byte exactly once.
func (g *game) paintTestPattern() {
	offset := byte(g.frame)
	for y := 0; y < nesHeight; y++ {
		for x := 0; x < nesWidth; x++ {
			i := (y*nesWidth + x) * 4
			g.framebuf[i+0] = byte(x) + offset
			g.framebuf[i+1] = byte(y) + offset
			g.framebuf[i+2] = byte(x ^ y)
			g.framebuf[i+3] = 0xFF
		}
	}
}

// simulateCPU burns CPU cycles at roughly the NES 2A03 rate (1.789773
// MHz). Goal: confirm Ebiten's 60 fps cadence isn't disrupted by a
// busy sibling goroutine. The body is a no-op atomic increment loop —
// it just measures whether the runtime / GC can keep ticks-per-second
// flat under contention.
func (g *game) simulateCPU() {
	const nesClockHz = 1_789_773
	const sliceCycles = nesClockHz / 60 // ~29830 cycles per frame
	sliceInterval := time.Second / 60

	ticker := time.NewTicker(sliceInterval)
	defer ticker.Stop()
	for range ticker.C {
		for i := 0; i < sliceCycles; i++ {
			g.cpuCycles.Add(1)
		}
	}
}

func main() {
	ebiten.SetWindowSize(nesWidth*scale, nesHeight*scale)
	ebiten.SetWindowTitle("spike-ebiten — booting…")
	ebiten.SetTPS(60)
	if err := ebiten.RunGame(newGame()); err != nil {
		log.Fatal(err)
	}
}
