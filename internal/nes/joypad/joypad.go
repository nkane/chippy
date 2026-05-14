// Package joypad models the standard NES controller pair at $4016 / $4017.
//
// Each controller is an 8-bit serial shift register. The CPU sees a single
// strobe line (write to $4016 bit 0) that is wired to both controllers in
// parallel; while the strobe line is high the shift register is held loaded
// with the live button state. The conventional driver pulses the strobe
// (write 1, then 0 to $4016) and then reads $4016 / $4017 eight times to
// shift out the buttons in order:
//
//	bit read 1 = A
//	bit read 2 = B
//	bit read 3 = Select
//	bit read 4 = Start
//	bit read 5 = Up
//	bit read 6 = Down
//	bit read 7 = Left
//	bit read 8 = Right
//
// After the eighth read the register is drained and real silicon reports 1
// for every subsequent read (the shift register fills from the top with 1s
// as bits clock out, because the serial input is tied high). chippy models
// this exactly.
//
// Only bit 0 of each read carries the controller data. Bits 1-4 are
// expansion-port lines on real hardware; ROMs typically AND the read with
// 1, so we return 0 for those bits.
package joypad

import "github.com/nkane/chippy/internal/cpu"

// Button names a logical NES controller button. Values are the shift-out
// order so `1 << Button` lines up with the live-state bitmap and the
// serial register.
type Button uint8

const (
	ButtonA Button = iota
	ButtonB
	ButtonSelect
	ButtonStart
	ButtonUp
	ButtonDown
	ButtonLeft
	ButtonRight
)

// Controller is one side of the joypad pair. Use Set to drive live state
// from the host; the Port wraps two Controllers and exposes them on the
// CPU bus.
type Controller struct {
	state uint8 // live button bitmap; bit n = Button(n) pressed
	shift uint8 // latched snapshot, shifted out one bit per read
}

// Set marks a button pressed or released. Safe to call between reads;
// the new state will not be visible to the CPU until the next strobe
// pulse latches it into the shift register.
func (c *Controller) Set(b Button, pressed bool) {
	if pressed {
		c.state |= 1 << b
	} else {
		c.state &^= 1 << b
	}
}

// State returns the live button bitmap. Exposed for tests and debug UI.
func (c *Controller) State() uint8 { return c.state }

// Port is the Peripheral that claims $4016-$4017 on the CPU bus and
// dispatches to two Controllers. The strobe line is shared: a write to
// $4016 bit 0 latches both controllers at once. Reads from $4016 shift
// out P1; reads from $4017 shift out P2.
type Port struct {
	P1, P2 Controller
	strobe bool
}

// New returns a fresh Port with both controllers cleared.
func New() *Port { return &Port{} }

// Range implements cpu.Peripheral. The port claims $4016-$4017.
//
// $4017 on real hardware is shared with APU frame-counter writes — that
// register is write-only on $4017 and reads come back to the controller.
// chippy will need to split write dispatch between the joypad port and
// the APU once the APU lands; for now $4017 writes are silent.
func (p *Port) Range() (uint16, uint16) { return 0x4016, 0x4017 }

// Read returns the next serial bit from the addressed controller. While
// the strobe line is high the register is continuously reloaded, so reads
// return the live A-button bit on every read; once the strobe goes low
// the snapshot taken on the rising edge shifts out one bit per read.
func (p *Port) Read(addr uint16) byte {
	switch addr {
	case 0x4016:
		return p.read(&p.P1)
	case 0x4017:
		return p.read(&p.P2)
	}
	return 0
}

func (p *Port) read(c *Controller) byte {
	if p.strobe {
		return c.state & 1
	}
	bit := c.shift & 1
	// Shift in a 1 from the top: matches the open-1 serial input on real
	// silicon, so reads past the eighth return 1.
	c.shift = (c.shift >> 1) | 0x80
	return bit
}

// Write handles the strobe line on $4016. Bit 0 controls the latch; the
// other bits drive expansion-port outputs that chippy does not model.
// $4017 writes belong to the APU frame counter — accepted here as a no-op
// until the APU peripheral overlaps this range.
func (p *Port) Write(addr uint16, v byte) {
	if addr != 0x4016 {
		return
	}
	newStrobe := v&1 != 0
	// While strobe is high the shift register is continuously reloaded.
	// We refresh on every high write and on the high→low transition the
	// last snapshot stays put, so capturing on every high write is the
	// simplest correct model.
	if newStrobe {
		p.P1.shift = p.P1.state
		p.P2.shift = p.P2.state
	}
	p.strobe = newStrobe
}

// compile-time check: Port satisfies cpu.Peripheral.
var _ cpu.Peripheral = (*Port)(nil)
