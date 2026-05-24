package apu

// VRC6Audio is the 3-channel audio expansion on Konami's VRC6
// mapper (mappers 24 / 26). Two pulse channels — like the 2A03's
// own but with seven duty selectors instead of four — plus a
// sawtooth channel driven by an 8-bit accumulator. Used heavily by
// Akumajou Densetsu (Castlevania III JP), Madara, and Esper Dream 2.
//
// Cart-side writes arrive via cart.VRC6AudioSink.Write(addr, v)
// with the "logical" addresses re-built by the cart so we don't
// need to know about VRC6a/b's swapped sub-bit routing:
//
//	$9000-$9002 — pulse 1 (volume + duty / period low / period high)
//	$A000-$A002 — pulse 2
//	$B000-$B002 — sawtooth (rate / period low / period high)
type VRC6Audio struct {
	pulse1 vrc6Pulse
	pulse2 vrc6Pulse
	saw    vrc6Sawtooth
}

type vrc6Pulse struct {
	enabled  bool
	mode     bool // 1 = ignore duty, output volume continuously
	duty     byte // 0..7 — 1/16 increments of duty cycle
	dutyStep byte // 0..15
	volume   byte
	period   uint16
	timer    uint16
}

type vrc6Sawtooth struct {
	enabled     bool
	rate        byte
	accumulator byte
	step        byte
	period      uint16
	timer       uint16
}

// NewVRC6Audio constructs a silent VRC6 audio chip.
func NewVRC6Audio() *VRC6Audio { return &VRC6Audio{} }

// Write implements cart.VRC6AudioSink. Each channel's three
// registers follow the same shape — vol/duty/control at offset 0,
// period low at 1, period high + enable at 2.
func (v *VRC6Audio) Write(addr uint16, val byte) {
	switch addr & 0xF003 {
	case 0x9000:
		v.pulse1.volume = val & 0x0F
		v.pulse1.duty = (val >> 4) & 0x07
		v.pulse1.mode = val&0x80 != 0
	case 0x9001:
		v.pulse1.period = (v.pulse1.period & 0x0F00) | uint16(val)
	case 0x9002:
		v.pulse1.period = (v.pulse1.period & 0x00FF) | uint16(val&0x0F)<<8
		v.pulse1.enabled = val&0x80 != 0
		if !v.pulse1.enabled {
			v.pulse1.dutyStep = 0
		}
	case 0xA000:
		v.pulse2.volume = val & 0x0F
		v.pulse2.duty = (val >> 4) & 0x07
		v.pulse2.mode = val&0x80 != 0
	case 0xA001:
		v.pulse2.period = (v.pulse2.period & 0x0F00) | uint16(val)
	case 0xA002:
		v.pulse2.period = (v.pulse2.period & 0x00FF) | uint16(val&0x0F)<<8
		v.pulse2.enabled = val&0x80 != 0
		if !v.pulse2.enabled {
			v.pulse2.dutyStep = 0
		}
	case 0xB000:
		v.saw.rate = val & 0x3F
	case 0xB001:
		v.saw.period = (v.saw.period & 0x0F00) | uint16(val)
	case 0xB002:
		v.saw.period = (v.saw.period & 0x00FF) | uint16(val&0x0F)<<8
		v.saw.enabled = val&0x80 != 0
		if !v.saw.enabled {
			v.saw.accumulator = 0
			v.saw.step = 0
		}
	}
}

// Step advances every channel by one CPU cycle.
func (v *VRC6Audio) Step() {
	v.pulse1.step()
	v.pulse2.step()
	v.saw.stepSaw()
}

func (p *vrc6Pulse) step() {
	if !p.enabled {
		return
	}
	if p.timer == 0 {
		p.timer = p.period
		p.dutyStep = (p.dutyStep + 1) & 0x0F
	} else {
		p.timer--
	}
}

func (p *vrc6Pulse) output() byte {
	if !p.enabled {
		return 0
	}
	if p.mode {
		// Mode bit forces constant output at volume.
		return p.volume
	}
	// Output volume on dutyStep <= duty, else 0. duty range 0..7
	// maps to 1/16..8/16 of the cycle.
	if p.dutyStep <= p.duty {
		return p.volume
	}
	return 0
}

func (s *vrc6Sawtooth) stepSaw() {
	if !s.enabled {
		return
	}
	if s.timer == 0 {
		s.timer = s.period
		s.step++
		if s.step >= 14 {
			s.step = 0
			s.accumulator = 0
		} else if s.step&1 == 0 {
			s.accumulator += s.rate
		}
	} else {
		s.timer--
	}
}

func (s *vrc6Sawtooth) output() byte {
	if !s.enabled {
		return 0
	}
	// Output the high 5 bits of the accumulator.
	return s.accumulator >> 3
}

// Output sums the three channels. Pulse outputs land in 0..15,
// sawtooth in 0..31. Total range: 0..61.
func (v *VRC6Audio) Output() int {
	return int(v.pulse1.output()) + int(v.pulse2.output()) + int(v.saw.output())
}
