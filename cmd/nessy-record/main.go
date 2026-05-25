// nessy-record renders a NES ROM headlessly into a GIF or MP4 — no
// Ebiten window, no OpenGL, no screen capture. It synthesizes the
// recording directly from the emulator: each frame's PPU framebuffer
// becomes a video frame, each frame's drained APU samples become the
// audio track, and a scripted input timeline drives the joypad. The
// result is deterministic — the same ROM + script produce a
// byte-identical recording every run, so it works as a PR smoke
// artifact without timing flake.
//
//	nessy-record -rom game.nes -frames 180 -o out.gif
//	nessy-record -rom game.nes -script in.json -o out.mp4   # video + audio
//
// GIF output uses only the Go stdlib. MP4 output (video + audio)
// shells out to ffmpeg — the same dependency the VHS smoke job
// already installs.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nkane/chippy/internal/nes"
	"github.com/nkane/chippy/internal/nes/apu"
	"github.com/nkane/chippy/internal/nes/ppu"
)

func main() {
	var (
		romPath = flag.String("rom", "", "iNES ROM path (positional arg also accepted)")
		frames  = flag.Int("frames", 180, "number of frames to record (60 = 1 second)")
		script  = flag.String("script", "", "JSON input timeline; omit for no input")
		out     = flag.String("o", "out.gif", "output file; .gif or .mp4 by extension")
	)
	flag.Parse()
	if *romPath == "" && flag.NArg() == 1 {
		*romPath = flag.Arg(0)
	}
	if *romPath == "" {
		fmt.Fprintln(os.Stderr, "usage: nessy-record [-rom PATH | PATH] [-frames N] [-script FILE] [-o OUT]")
		os.Exit(2)
	}

	if err := run(*romPath, *frames, *script, *out); err != nil {
		fmt.Fprintln(os.Stderr, "nessy-record:", err)
		os.Exit(1)
	}
}

func run(romPath string, frames int, scriptPath, out string) error {
	romBytes, err := os.ReadFile(romPath)
	if err != nil {
		return err
	}
	rom, err := nes.ParseBytes(romBytes)
	if err != nil {
		return fmt.Errorf("parse ROM: %w", err)
	}
	b, err := buildBus(rom)
	if err != nil {
		return fmt.Errorf("build NES: %w", err)
	}
	script, err := loadScript(scriptPath)
	if err != nil {
		return fmt.Errorf("load script: %w", err)
	}

	// Capture each frame's framebuffer + audio batch. Both come from
	// the same step so they stay in sync.
	vid := make([][]byte, 0, frames)
	var pcm []int16
	for f := 0; f < frames; f++ {
		script.applyAt(&b.joy.P1, f)
		b.stepFrame()
		fb := b.ppu.FrameBuffer()
		frame := make([]byte, len(fb))
		copy(frame, fb)
		vid = append(vid, frame)
		pcm = append(pcm, b.apu.Samples()...)
	}

	switch strings.ToLower(filepath.Ext(out)) {
	case ".gif":
		return writeGIF(out, vid)
	case ".mp4":
		return writeMP4(out, vid, pcm)
	default:
		return fmt.Errorf("unsupported output extension %q (use .gif or .mp4)", filepath.Ext(out))
	}
}

// writeGIF encodes the captured RGBA frames into an animated GIF.
// NES output is at most 64 distinct colours, so a palette built from
// the frames fits well under GIF's 256-entry limit with no lossy
// quantisation.
func writeGIF(path string, vid [][]byte) error {
	pal := buildPalette(vid)
	colorIndex := make(map[color.RGBA]uint8, len(pal))
	for i, c := range pal {
		rc := c.(color.RGBA)
		colorIndex[rc] = uint8(i)
	}

	g := &gif.GIF{}
	rect := image.Rect(0, 0, ppu.ScreenWidth, ppu.ScreenHeight)
	for _, frame := range vid {
		img := image.NewPaletted(rect, pal)
		for px := 0; px < ppu.ScreenWidth*ppu.ScreenHeight; px++ {
			off := px * 4
			c := color.RGBA{frame[off], frame[off+1], frame[off+2], 0xFF}
			img.Pix[px] = colorIndex[c]
		}
		g.Image = append(g.Image, img)
		// GIF delay is in centiseconds; 60 fps (1.67 cs) isn't
		// representable, so 2 cs (~50 fps) is the closest smooth
		// integer. Good enough for a preview artifact.
		g.Delay = append(g.Delay, 2)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return gif.EncodeAll(f, g)
}

// buildPalette collects the unique colours across all frames into a
// color.Palette. Caps at 256 (GIF limit); NES never exceeds 64 so
// the cap is defensive.
func buildPalette(vid [][]byte) color.Palette {
	seen := map[color.RGBA]bool{}
	pal := color.Palette{}
	for _, frame := range vid {
		for px := 0; px < ppu.ScreenWidth*ppu.ScreenHeight; px++ {
			off := px * 4
			c := color.RGBA{frame[off], frame[off+1], frame[off+2], 0xFF}
			if !seen[c] {
				seen[c] = true
				pal = append(pal, c)
				if len(pal) == 256 {
					return pal
				}
			}
		}
	}
	if len(pal) == 0 {
		pal = append(pal, color.RGBA{0, 0, 0, 0xFF})
	}
	return pal
}

// writeMP4 muxes the captured video + audio into an H.264/AAC MP4 via
// ffmpeg. Video is fed as raw RGBA at 60 fps; audio as signed 16-bit
// LE mono at the APU's sample rate. Requires ffmpeg on PATH.
func writeMP4(path string, vid [][]byte, pcm []int16) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("mp4 output needs ffmpeg on PATH: %w", err)
	}
	dir, err := os.MkdirTemp("", "nessy-record-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	videoRaw := filepath.Join(dir, "video.raw")
	vf, err := os.Create(videoRaw)
	if err != nil {
		return err
	}
	for _, frame := range vid {
		if _, err := vf.Write(frame); err != nil {
			vf.Close()
			return err
		}
	}
	vf.Close()

	audioRaw := filepath.Join(dir, "audio.raw")
	af, err := os.Create(audioRaw)
	if err != nil {
		return err
	}
	buf := make([]byte, len(pcm)*2)
	for i, s := range pcm {
		buf[i*2] = byte(s)
		buf[i*2+1] = byte(s >> 8)
	}
	if _, err := af.Write(buf); err != nil {
		af.Close()
		return err
	}
	af.Close()

	args := []string{
		"-y",
		"-f", "rawvideo", "-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", ppu.ScreenWidth, ppu.ScreenHeight),
		"-framerate", "60", "-i", videoRaw,
		"-f", "s16le", "-ar", fmt.Sprintf("%d", apu.SampleRate), "-ac", "1", "-i", audioRaw,
		"-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-vf", "scale=512:480:flags=neighbor", // 2× nearest-neighbor upscale
		"-c:a", "aac",
		"-shortest",
		path,
	}
	cmd := exec.Command("ffmpeg", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
