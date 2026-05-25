//go:build nessy

package main

import (
	"fmt"
	"os"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/nkane/chippy/internal/nes/joypad"
)

// gamepadMap maps Ebiten standard-layout gamepad buttons to NES
// joypad buttons. Standard layout positions:
//
//	RightBottom — bottom face button (Xbox A / DualSense ✕) → NES A
//	RightRight  — right face button  (Xbox B / DualSense ○) → NES B
//	CenterRight — start button                                → Start
//	CenterLeft  — select / share / back button                → Select
//	LeftTop / Bottom / Left / Right — D-pad
//
// Analog-stick fallback for the D-pad lives in pollGamepadStick.
var gamepadMap = []struct {
	std ebiten.StandardGamepadButton
	btn joypad.Button
}{
	{ebiten.StandardGamepadButtonLeftTop, joypad.ButtonUp},
	{ebiten.StandardGamepadButtonLeftBottom, joypad.ButtonDown},
	{ebiten.StandardGamepadButtonLeftLeft, joypad.ButtonLeft},
	{ebiten.StandardGamepadButtonLeftRight, joypad.ButtonRight},
	{ebiten.StandardGamepadButtonRightBottom, joypad.ButtonA},
	{ebiten.StandardGamepadButtonRightRight, joypad.ButtonB},
	{ebiten.StandardGamepadButtonCenterRight, joypad.ButtonStart},
	{ebiten.StandardGamepadButtonCenterLeft, joypad.ButtonSelect},
}

// gamepadStickThreshold is the deadzone past which the analog
// stick contributes to a D-pad direction. 0.5 is comfortable on
// most pads — small drift stays neutral, deliberate flicks register.
const gamepadStickThreshold = 0.5

// gamepadConnState tracks per-frame connect / disconnect transitions
// so we can stderr-notify the user once per change instead of every
// frame.
type gamepadConnState struct {
	prev map[ebiten.GamepadID]bool
	buf  []ebiten.GamepadID
}

// pollGamepad routes input from the first standard-layout gamepad
// onto P1, ORing into whatever the keyboard already set so a player
// can mash both. No-op when no standard pad is connected.
func (s *gamepadConnState) pollGamepad(p1 *joypad.Controller) {
	s.buf = ebiten.AppendGamepadIDs(s.buf[:0])
	cur := make(map[ebiten.GamepadID]bool, len(s.buf))
	for _, id := range s.buf {
		cur[id] = true
		if !s.prev[id] {
			name := ebiten.GamepadName(id)
			if name == "" {
				name = "unknown"
			}
			fmt.Fprintf(os.Stderr, "nessy: gamepad %d connected (%s, standard=%v)\n",
				id, name, ebiten.IsStandardGamepadLayoutAvailable(id))
		}
	}
	for id := range s.prev {
		if !cur[id] {
			fmt.Fprintf(os.Stderr, "nessy: gamepad %d disconnected\n", id)
		}
	}
	s.prev = cur

	// Pick the first connected pad with a standard layout to drive
	// P1. Pads without a standard layout get ignored — the user can
	// fall back to keyboard until a future per-pad mapping path
	// lands.
	var p1id ebiten.GamepadID = -1
	standard := false
	for _, id := range s.buf {
		if ebiten.IsStandardGamepadLayoutAvailable(id) {
			p1id = id
			standard = true
			break
		}
	}
	if p1id == -1 || !standard {
		return
	}

	for _, m := range gamepadMap {
		if ebiten.IsStandardGamepadButtonPressed(p1id, m.std) {
			p1.Set(m.btn, true)
		}
	}
	// Left stick → D-pad. axisH < 0 = left, > 0 = right; axisV < 0 = up.
	axisH := ebiten.StandardGamepadAxisValue(p1id, ebiten.StandardGamepadAxisLeftStickHorizontal)
	axisV := ebiten.StandardGamepadAxisValue(p1id, ebiten.StandardGamepadAxisLeftStickVertical)
	if axisH < -gamepadStickThreshold {
		p1.Set(joypad.ButtonLeft, true)
	}
	if axisH > gamepadStickThreshold {
		p1.Set(joypad.ButtonRight, true)
	}
	if axisV < -gamepadStickThreshold {
		p1.Set(joypad.ButtonUp, true)
	}
	if axisV > gamepadStickThreshold {
		p1.Set(joypad.ButtonDown, true)
	}
}
