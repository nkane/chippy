package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const demoROM = "../../roms/demos/vblank-bounce/vblank-bounce.nes"

// GIF output is stdlib-only, so it works on every runner. Assert the
// run produces a non-trivial GIF with the magic header.
func TestRecord_GIF(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.gif")
	if err := run(demoROM, 30, "", out); err != nil {
		t.Fatalf("run gif: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read gif: %v", err)
	}
	if len(data) < 100 {
		t.Errorf("gif suspiciously small: %d bytes", len(data))
	}
	if string(data[:6]) != "GIF89a" {
		t.Errorf("bad GIF magic: %q", data[:6])
	}
}

// Deterministic: the same ROM + frame count produce a byte-identical
// GIF every run. That's what makes the recording usable as a stable
// CI artifact.
func TestRecord_GIF_Deterministic(t *testing.T) {
	a := filepath.Join(t.TempDir(), "a.gif")
	b := filepath.Join(t.TempDir(), "b.gif")
	if err := run(demoROM, 20, "", a); err != nil {
		t.Fatalf("run a: %v", err)
	}
	if err := run(demoROM, 20, "", b); err != nil {
		t.Fatalf("run b: %v", err)
	}
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if string(da) != string(db) {
		t.Errorf("recording not deterministic: %d vs %d bytes differ", len(da), len(db))
	}
}

// MP4 output needs ffmpeg — skip when absent so the test stays green
// on minimal runners.
func TestRecord_MP4(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := run(demoROM, 30, "", out); err != nil {
		t.Fatalf("run mp4: %v", err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat mp4: %v", err)
	}
	if info.Size() < 100 {
		t.Errorf("mp4 suspiciously small: %d bytes", info.Size())
	}
}

// Input script parses + rejects unknown buttons.
func TestScript_ParseAndReject(t *testing.T) {
	good := filepath.Join(t.TempDir(), "good.json")
	if err := os.WriteFile(good, []byte(`{"10":["A"],"20":["Right","A"],"25":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := loadScript(good)
	if err != nil {
		t.Fatalf("loadScript good: %v", err)
	}
	if len(s.frames) != 3 {
		t.Errorf("parsed %d keyframes; want 3", len(s.frames))
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"10":["Turbo"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadScript(bad); err == nil {
		t.Errorf("expected error for unknown button name")
	}
}

// Empty script path → no input, no error.
func TestScript_EmptyPath(t *testing.T) {
	s, err := loadScript("")
	if err != nil {
		t.Fatalf("loadScript empty: %v", err)
	}
	if len(s.frames) != 0 {
		t.Errorf("empty script has %d frames; want 0", len(s.frames))
	}
}
