package joypad

// FullState captures the joypad Port's mutable state for nessy
// save states (#266). frameCounterSink isn't persisted — it's
// re-bound from post-restore wiring.
type FullState struct {
	P1State, P1Shift byte
	P2State, P2Shift byte
	Strobe           bool
}

// SaveFullState captures Port state.
func (p *Port) SaveFullState() FullState {
	return FullState{
		P1State: p.P1.state, P1Shift: p.P1.shift,
		P2State: p.P2.state, P2Shift: p.P2.shift,
		Strobe: p.strobe,
	}
}

// LoadFullState overwrites Port state from s.
func (p *Port) LoadFullState(s FullState) {
	p.P1.state, p.P1.shift = s.P1State, s.P1Shift
	p.P2.state, p.P2.shift = s.P2State, s.P2Shift
	p.strobe = s.Strobe
}
