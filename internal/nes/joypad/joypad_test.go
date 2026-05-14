package joypad

import "testing"

// pulseStrobe drives $4016 high then low — the standard pattern a driver
// uses to latch the current button state into both controllers' shift
// registers.
func pulseStrobe(p *Port) {
	p.Write(0x4016, 0x01)
	p.Write(0x4016, 0x00)
}

// readBit reads $4016 / $4017 and returns the data bit (bit 0).
func readBit(p *Port, addr uint16) byte { return p.Read(addr) & 1 }

// All buttons pressed → eight consecutive reads each return 1; the ninth
// read and beyond return 1 (post-shift open-1 silicon behavior).
func TestPort_ShiftOrderAllPressed(t *testing.T) {
	p := New()
	for b := ButtonA; b <= ButtonRight; b++ {
		p.P1.Set(b, true)
	}
	pulseStrobe(p)
	for i := range 8 {
		if got := readBit(p, 0x4016); got != 1 {
			t.Errorf("read %d: got %d, want 1", i, got)
		}
	}
	// Post-end reads must return 1 (open-1).
	for i := range 4 {
		if got := readBit(p, 0x4016); got != 1 {
			t.Errorf("post-shift read %d: got %d, want 1", i, got)
		}
	}
}

// Buttons read out in A, B, Select, Start, Up, Down, Left, Right order.
// Press only A and Start; the sequence should be 1,0,0,1,0,0,0,0.
func TestPort_ShiftOrderSelective(t *testing.T) {
	p := New()
	p.P1.Set(ButtonA, true)
	p.P1.Set(ButtonStart, true)
	pulseStrobe(p)
	want := []byte{1, 0, 0, 1, 0, 0, 0, 0}
	for i, w := range want {
		if got := readBit(p, 0x4016); got != w {
			t.Errorf("read %d (%s): got %d, want %d", i, buttonName(Button(i)), got, w)
		}
	}
}

// While the strobe line is held high the shift register is reloaded
// every cycle, so reads return the live A-button bit on every read
// regardless of how many reads have happened.
func TestPort_StrobeHighReturnsAContinuously(t *testing.T) {
	p := New()
	p.Write(0x4016, 0x01) // strobe HIGH, leave high
	// A not pressed: reads return 0 forever.
	for i := range 16 {
		if got := readBit(p, 0x4016); got != 0 {
			t.Errorf("strobe-high, A released, read %d = %d; want 0", i, got)
		}
	}
	p.P1.Set(ButtonA, true)
	// A pressed: reads return 1 forever (no shifting).
	for i := range 16 {
		if got := readBit(p, 0x4016); got != 1 {
			t.Errorf("strobe-high, A pressed, read %d = %d; want 1", i, got)
		}
	}
}

// $4016 and $4017 latch on the same strobe pulse but shift out their own
// controller's state. Press different buttons on each and verify the
// streams don't cross-contaminate.
func TestPort_P1AndP2Independent(t *testing.T) {
	p := New()
	p.P1.Set(ButtonA, true)     // bit 0
	p.P2.Set(ButtonStart, true) // bit 3
	pulseStrobe(p)
	p1Want := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	p2Want := []byte{0, 0, 0, 1, 0, 0, 0, 0}
	for i := range 8 {
		if got := readBit(p, 0x4016); got != p1Want[i] {
			t.Errorf("P1 read %d: got %d, want %d", i, got, p1Want[i])
		}
		if got := readBit(p, 0x4017); got != p2Want[i] {
			t.Errorf("P2 read %d: got %d, want %d", i, got, p2Want[i])
		}
	}
}

// Changing button state between a latch and the read sequence must not
// affect the in-flight shift — the snapshot is taken on the strobe pulse,
// not on each read.
func TestPort_StateLatchedAtStrobe(t *testing.T) {
	p := New()
	p.P1.Set(ButtonA, true)
	pulseStrobe(p) // latch A=1
	p.P1.Set(ButtonA, false)
	p.P1.Set(ButtonStart, true)
	// First read should still see A=1 (latched), Start should not appear
	// in the bit-3 slot of this read cycle.
	want := []byte{1, 0, 0, 0, 0, 0, 0, 0}
	for i, w := range want {
		if got := readBit(p, 0x4016); got != w {
			t.Errorf("read %d after mid-shift Set: got %d, want %d", i, got, w)
		}
	}
	// New latch picks up Start.
	pulseStrobe(p)
	got := []byte{}
	for range 8 {
		got = append(got, readBit(p, 0x4016))
	}
	want2 := []byte{0, 0, 0, 1, 0, 0, 0, 0}
	for i := range want2 {
		if got[i] != want2[i] {
			t.Errorf("post-relatch read %d: got %d, want %d", i, got[i], want2[i])
		}
	}
}

// Set with pressed=false clears the bit, even when other buttons remain
// pressed.
func TestController_SetReleases(t *testing.T) {
	var c Controller
	c.Set(ButtonA, true)
	c.Set(ButtonStart, true)
	if c.State() != (1<<ButtonA)|(1<<ButtonStart) {
		t.Fatalf("state setup wrong: %08b", c.State())
	}
	c.Set(ButtonA, false)
	if c.State() != 1<<ButtonStart {
		t.Errorf("after release A, state = %08b; want %08b", c.State(), 1<<ButtonStart)
	}
}

// Range claims exactly $4016-$4017.
func TestPort_Range(t *testing.T) {
	p := New()
	lo, hi := p.Range()
	if lo != 0x4016 || hi != 0x4017 {
		t.Errorf("Range = $%04X-$%04X; want $4016-$4017", lo, hi)
	}
}

// Writes outside $4016 must not latch the strobe (so an APU write to
// $4017 cannot accidentally re-latch the joypad).
func TestPort_WriteTo4017DoesNotLatch(t *testing.T) {
	p := New()
	p.P1.Set(ButtonA, true)
	p.Write(0x4017, 0x01) // would-be strobe on the wrong register
	// Without an explicit pulseStrobe on $4016, the shift register is
	// still empty — reads should return 0 then 1s (post-shift).
	if got := readBit(p, 0x4016); got != 0 {
		t.Errorf("$4017 write must not latch; got first read = %d", got)
	}
}

func buttonName(b Button) string {
	switch b {
	case ButtonA:
		return "A"
	case ButtonB:
		return "B"
	case ButtonSelect:
		return "Select"
	case ButtonStart:
		return "Start"
	case ButtonUp:
		return "Up"
	case ButtonDown:
		return "Down"
	case ButtonLeft:
		return "Left"
	case ButtonRight:
		return "Right"
	}
	return "?"
}
