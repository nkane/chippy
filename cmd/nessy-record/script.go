package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// inputScript is a sparse timeline of held-button sets keyed by frame
// index. Each entry replaces the full P1 held-button state from that
// frame onward until the next entry — a "keyframe" model, so a quick
// tap is two entries (press frame, release frame).
//
// JSON wire format:
//
//	{ "30": ["A"], "33": [], "60": ["Right", "A"] }
//
// Button names (case-insensitive): A B Select Start Up Down Left Right.
type inputScript struct {
	frames []scriptEntry // sorted ascending by frame
}

type scriptEntry struct {
	frame   int
	buttons []joypad.Button
}

// loadScript parses a JSON timeline file. Empty path → empty script
// (no input). Unknown button names error so a typo in the timeline
// doesn't silently drop input.
func loadScript(path string) (*inputScript, error) {
	if path == "" {
		return &inputScript{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string][]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse script: %w", err)
	}
	s := &inputScript{}
	for frameStr, btnNames := range raw {
		frame, err := strconv.Atoi(frameStr)
		if err != nil {
			return nil, fmt.Errorf("script frame key %q not an integer: %w", frameStr, err)
		}
		var btns []joypad.Button
		for _, name := range btnNames {
			b, ok := parseButton(name)
			if !ok {
				return nil, fmt.Errorf("script frame %d: unknown button %q", frame, name)
			}
			btns = append(btns, b)
		}
		s.frames = append(s.frames, scriptEntry{frame: frame, buttons: btns})
	}
	sort.Slice(s.frames, func(i, j int) bool { return s.frames[i].frame <= s.frames[j].frame })
	return s, nil
}

// applyAt sets P1's held buttons for the given frame. Walks to the
// latest keyframe at or before f + applies its set; clears all
// buttons not in that set. Cheap linear scan — scripts are short.
func (s *inputScript) applyAt(p1 *joypad.Controller, f int) {
	var active []joypad.Button
	for _, e := range s.frames {
		if e.frame > f {
			break
		}
		active = e.buttons
	}
	// Clear every button, then set the active set — keeps releases
	// deterministic regardless of prior state.
	for b := joypad.Button(0); b <= joypad.ButtonRight; b++ {
		p1.Set(b, false)
	}
	for _, b := range active {
		p1.Set(b, true)
	}
}

// parseButton maps a button name (case-insensitive, whitespace-
// tolerant) to the joypad constant.
func parseButton(name string) (joypad.Button, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
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
