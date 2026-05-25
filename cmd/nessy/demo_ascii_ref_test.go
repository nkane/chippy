package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// asciiRefUpdate regenerates the committed ascii references instead of
// comparing. Run: go test -run TestDemo_ASCIIReference -asciiref-update ./cmd/nessy/...
var asciiRefUpdate = flag.Bool("asciiref-update", false, "rewrite demo ascii reference goldens")

// The per-cycle CPU↔PPU rewrite (#342, docs/plans/per-cycle-cpu-ppu.md)
// shifts PPU phase, so the SHA-pinned demo framebuffers will change. A
// raw SHA can't tell "rendered the same picture, one frame later" from
// "rendered garbage". This harness dumps each visual demo's framebuffer
// as a brightness-ramp ascii grid into a committed golden, so after the
// rewrite re-pins the SHAs we can diff the *picture* and confirm it's
// still the demo it was.
//
// Goldens use .golden so .gitattributes pins them to LF (Windows CI).
type asciiDemo struct {
	name   string
	rom    string // path segments under roms/demos/
	frames int
	setup  func(*nesBus)
}

func asciiDemos() []asciiDemo {
	return []asciiDemo{
		{"hello-bg", "hello-bg/hello-bg.nes", 5, nil},
		{"oam-grid", "oam-grid/oam-grid.nes", 5, nil},
		{"mmc1-banks-early", "mmc1-banks/mmc1-banks.nes", 5, nil},
		{"mmc1-banks-late", "mmc1-banks/mmc1-banks.nes", 45, nil},
		{"scroll-split", "scroll-split/scroll-split.nes", 10, nil},
		{"mmc3-split", "mmc3-split/mmc3-split.nes", 10, nil},
		{"vblank-bounce-early", "vblank-bounce/vblank-bounce.nes", 5, nil},
		{"vblank-bounce-late", "vblank-bounce/vblank-bounce.nes", 30, nil},
		{"input-echo-idle", "input-echo/input-echo.nes", 7, nil},
		{"input-echo-up", "input-echo/input-echo.nes", 7, func(b *nesBus) {
			b.joy.P1.Set(joypad.ButtonUp, true)
		}},
	}
}

func TestDemo_ASCIIReference(t *testing.T) {
	for _, d := range asciiDemos() {
		t.Run(d.name, func(t *testing.T) {
			romPath := filepath.Join(append([]string{"..", "..", "roms", "demos"}, strings.Split(d.rom, "/")...)...)
			fb, _ := runDemoFramesInternal(t, romPath, d.frames, d.setup)
			got := asciiFrame(fb)

			golden := filepath.Join("testdata", "demo-ascii", d.name+".golden")
			if *asciiRefUpdate {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", golden)
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -asciiref-update): %v", err)
			}
			if got != string(want) {
				t.Errorf("%s picture diverged from reference:\n--- got ---\n%s", d.name, got)
			}
		})
	}
}

// asciiFrame renders a 256×240 RGBA framebuffer as a 64×60 brightness-
// ramp grid. Each cell is the *mean* luminance of its 4×4 block — so a
// thin white stroke (text, sprite edge) lifts the cell instead of being
// skipped by point-sampling. The 10-level ramp keeps real layout, not
// just "lit vs dark".
func asciiFrame(fb []byte) string {
	const ramp = " .:-=+*#%@"
	var b strings.Builder
	for y := 0; y < 240; y += 4 {
		for x := 0; x < 256; x += 4 {
			sum := 0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 4; dx++ {
					off := ((y+dy)*256 + (x + dx)) * 4
					sum += int(fb[off]) + int(fb[off+1]) + int(fb[off+2])
				}
			}
			lum := sum / 16 // mean per-pixel luminance, 0..765
			idx := lum * (len(ramp) - 1) / 765
			b.WriteByte(ramp[idx])
		}
		b.WriteByte('\n')
	}
	return b.String()
}
