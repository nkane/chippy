//go:build nessy

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// controllerConfigPath returns ~/.nessy/controller.json. Missing
// file → defaults; malformed file → defaults + a warning.
func controllerConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nessy", "controller.json")
}

// controllerConfig is the wire format the user edits by hand. Each
// per-player section maps NES button names (Up/Down/Left/Right/A/
// B/Start/Select) to ebiten key names (ArrowUp / Z / etc — exactly
// the names the ebiten.Key.String() emits). Missing entries fall
// back to the built-in defaults.
type controllerConfig struct {
	P1 map[string]string `json:"p1,omitempty"`
	// P2 reserved — joypad.Port has the slot but no demo currently
	// wires P2 to the host. Honored when present so the user can
	// pre-stage their layout.
	P2 map[string]string `json:"p2,omitempty"`
}

// loadControllerConfig reads + parses the JSON file. Returns
// nil + no error when the file is absent.
func loadControllerConfig() (*controllerConfig, error) {
	path := controllerConfigPath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cfg controllerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// applyControllerConfig overlays cfg onto the package-level keyMap
// (the P1 bindings the game-loop's pollInput consults). Unknown key
// or button names print a warning + leave the default in place.
func applyControllerConfig(cfg *controllerConfig) {
	if cfg == nil || len(cfg.P1) == 0 {
		return
	}
	overrides := make(map[joypad.Button]ebiten.Key, len(cfg.P1))
	for btnName, keyName := range cfg.P1 {
		btn, ok := parseButton(btnName)
		if !ok {
			fmt.Fprintf(os.Stderr, "nessy: controller.json: unknown button %q\n", btnName)
			continue
		}
		key, ok := parseKey(keyName)
		if !ok {
			fmt.Fprintf(os.Stderr, "nessy: controller.json: unknown key %q\n", keyName)
			continue
		}
		overrides[btn] = key
	}
	for i := range keyMap {
		if k, ok := overrides[keyMap[i].btn]; ok {
			keyMap[i].key = k
		}
	}
}

// parseButton maps NES button names (case-insensitive) to the
// joypad.Button constants.
func parseButton(name string) (joypad.Button, bool) {
	switch normalize(name) {
	case "a":
		return joypad.ButtonA, true
	case "b":
		return joypad.ButtonB, true
	case "select":
		return joypad.ButtonSelect, true
	case "start":
		return joypad.ButtonStart, true
	case "up":
		return joypad.ButtonUp, true
	case "down":
		return joypad.ButtonDown, true
	case "left":
		return joypad.ButtonLeft, true
	case "right":
		return joypad.ButtonRight, true
	}
	return 0, false
}

// parseKey maps an ebiten key name (the same string that
// ebiten.Key.String() returns) to its constant. Walks the full key
// range once on each call — N is small, no need to memoise.
func parseKey(name string) (ebiten.Key, bool) {
	target := normalize(name)
	for k := ebiten.Key(0); k <= ebiten.KeyMax; k++ {
		if normalize(k.String()) == target {
			return k, true
		}
	}
	return 0, false
}

// normalize lower-cases + strips trailing whitespace. Tolerant of
// hand-edited JSON where users write "Arrow Up" or " A " etc.
func normalize(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r-'A'+'a'))
		case r == ' ' || r == '\t' || r == '_' || r == '-':
			// drop
		default:
			out = append(out, byte(r))
		}
	}
	return string(out)
}
