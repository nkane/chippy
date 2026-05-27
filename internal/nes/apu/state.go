package apu

import "errors"

// FullState is the gob-serializable APU capture for nessy save
// states (#266). Holds every per-channel field plus frame counter
// state. The sample ring is NOT part of state — it's transient
// audio backlog that the host drains every frame; restoring it
// would just create a phantom audio burst.
type FullState struct {
	Pulse1   PulseState
	Pulse2   PulseState
	Triangle TriangleState
	Noise    NoiseState
	DMC      DMCState

	Mode4Step    bool
	IRQInhibit   bool
	FrameStep    int
	FrameTimer   int
	FrameIRQFlag bool

	// $4017-write delay state. Non-zero FrameResetDelay means a
	// counter reset is pending; FrameResetValue holds the deferred
	// $4017 byte to latch when the countdown expires.
	FrameResetDelay int
	FrameResetValue byte

	AlternateTick bool
	SampleAccum   int
}

// PulseState mirrors pulseChannel with exported fields.
type PulseState struct {
	Enabled         bool
	ChannelTwo      bool
	Duty, DutyStep  byte
	Timer, Period   uint16
	LengthHalt      bool
	LengthCounter   byte
	EnvelopeStart   bool
	EnvelopeLoop    bool
	EnvelopeConst   bool
	EnvelopeVolume  byte
	EnvelopeDivider byte
	EnvelopeDecay   byte
	SweepEnabled    bool
	SweepNegate     bool
	SweepShift      byte
	SweepPeriod     byte
	SweepDivider    byte
	SweepReload     bool
}

// TriangleState mirrors triangleChannel.
type TriangleState struct {
	Enabled       bool
	LinearReload  bool
	LinearReloadV byte
	LinearControl bool
	LinearCounter byte
	LengthCounter byte
	Timer         uint16
	Period        uint16
	SequencerStep byte
}

// NoiseState mirrors noiseChannel.
type NoiseState struct {
	Enabled          bool
	LengthHalt       bool
	EnvelopeLoop     bool
	EnvelopeConstant bool
	EnvelopeVolume   byte
	EnvelopeStart    bool
	EnvelopeDivider  byte
	EnvelopeDecay    byte
	LengthCounter    byte
	Timer            uint16
	Period           uint16
	LFSR             uint16
	ShortMode        bool
}

// DMCState mirrors dmcChannel.
type DMCState struct {
	Enabled        bool
	IRQEnable      bool
	Loop           bool
	RateIdx        byte
	Output         byte
	SampleAddrBase uint16
	SampleLenBase  uint16
	CurrentAddr    uint16
	BytesRemaining uint16
	SampleBuffer   byte
	BufferEmpty    bool
	ShiftRegister  byte
	BitsRemaining  byte
	Silenced       bool
	Timer          uint16
	IRQPending     bool
}

// SaveFullState copies the APU's mutable state into a FullState.
func (a *APU) SaveFullState() FullState {
	return FullState{
		Pulse1:          a.pulse1.save(),
		Pulse2:          a.pulse2.save(),
		Triangle:        a.triangle.save(),
		Noise:           a.noise.save(),
		DMC:             a.dmc.save(),
		Mode4Step:       a.mode4Step,
		IRQInhibit:      a.irqInhibit,
		FrameStep:       a.frameStep,
		FrameTimer:      a.frameTimer,
		FrameIRQFlag:    a.frameIRQFlag,
		FrameResetDelay: a.frameResetDelay,
		FrameResetValue: a.frameResetValue,
		AlternateTick:   a.alternateTick,
		SampleAccum:     a.sampleAccum,
	}
}

// LoadFullState overwrites the APU's state from s. The DMC's
// CPU-bus + staller bindings + the IRQ sink stay connected from the
// post-restore wiring. The sample ring isn't touched.
func (a *APU) LoadFullState(s FullState) error {
	a.pulse1.load(s.Pulse1)
	a.pulse2.load(s.Pulse2)
	a.triangle.load(s.Triangle)
	a.noise.load(s.Noise)
	a.dmc.load(s.DMC)
	a.mode4Step = s.Mode4Step
	a.irqInhibit = s.IRQInhibit
	a.frameStep = s.FrameStep
	a.frameTimer = s.FrameTimer
	a.frameIRQFlag = s.FrameIRQFlag
	a.frameResetDelay = s.FrameResetDelay
	a.frameResetValue = s.FrameResetValue
	a.alternateTick = s.AlternateTick
	a.sampleAccum = s.SampleAccum
	return nil
}

func (p *pulseChannel) save() PulseState {
	return PulseState{
		Enabled: p.enabled, ChannelTwo: p.channelTwo,
		Duty: p.duty, DutyStep: p.dutyStep,
		Timer: p.timer, Period: p.period,
		LengthHalt: p.lengthHalt, LengthCounter: p.lengthCounter,
		EnvelopeStart: p.envelopeStart, EnvelopeLoop: p.envelopeLoop,
		EnvelopeConst: p.envelopeConstant, EnvelopeVolume: p.envelopeVolume,
		EnvelopeDivider: p.envelopeDivider, EnvelopeDecay: p.envelopeDecay,
		SweepEnabled: p.sweepEnabled, SweepNegate: p.sweepNegate,
		SweepShift: p.sweepShift, SweepPeriod: p.sweepPeriod,
		SweepDivider: p.sweepDivider, SweepReload: p.sweepReload,
	}
}

func (p *pulseChannel) load(s PulseState) {
	p.enabled = s.Enabled
	p.channelTwo = s.ChannelTwo
	p.duty = s.Duty
	p.dutyStep = s.DutyStep
	p.timer = s.Timer
	p.period = s.Period
	p.lengthHalt = s.LengthHalt
	p.lengthCounter = s.LengthCounter
	p.envelopeStart = s.EnvelopeStart
	p.envelopeLoop = s.EnvelopeLoop
	p.envelopeConstant = s.EnvelopeConst
	p.envelopeVolume = s.EnvelopeVolume
	p.envelopeDivider = s.EnvelopeDivider
	p.envelopeDecay = s.EnvelopeDecay
	p.sweepEnabled = s.SweepEnabled
	p.sweepNegate = s.SweepNegate
	p.sweepShift = s.SweepShift
	p.sweepPeriod = s.SweepPeriod
	p.sweepDivider = s.SweepDivider
	p.sweepReload = s.SweepReload
}

func (t *triangleChannel) save() TriangleState {
	return TriangleState{
		Enabled:      t.enabled,
		LinearReload: t.linearReload, LinearReloadV: t.linearReloadV,
		LinearControl: t.linearControl, LinearCounter: t.linearCounter,
		LengthCounter: t.lengthCounter,
		Timer:         t.timer, Period: t.period,
		SequencerStep: t.sequencerStep,
	}
}

func (t *triangleChannel) load(s TriangleState) {
	t.enabled = s.Enabled
	t.linearReload = s.LinearReload
	t.linearReloadV = s.LinearReloadV
	t.linearControl = s.LinearControl
	t.linearCounter = s.LinearCounter
	t.lengthCounter = s.LengthCounter
	t.timer = s.Timer
	t.period = s.Period
	t.sequencerStep = s.SequencerStep
}

func (n *noiseChannel) save() NoiseState {
	return NoiseState{
		Enabled:    n.enabled,
		LengthHalt: n.lengthHalt, EnvelopeLoop: n.envelopeLoop,
		EnvelopeConstant: n.envelopeConstant, EnvelopeVolume: n.envelopeVolume,
		EnvelopeStart: n.envelopeStart, EnvelopeDivider: n.envelopeDivider,
		EnvelopeDecay: n.envelopeDecay, LengthCounter: n.lengthCounter,
		Timer: n.timer, Period: n.period,
		LFSR: n.lfsr, ShortMode: n.shortMode,
	}
}

func (n *noiseChannel) load(s NoiseState) {
	n.enabled = s.Enabled
	n.lengthHalt = s.LengthHalt
	n.envelopeLoop = s.EnvelopeLoop
	n.envelopeConstant = s.EnvelopeConstant
	n.envelopeVolume = s.EnvelopeVolume
	n.envelopeStart = s.EnvelopeStart
	n.envelopeDivider = s.EnvelopeDivider
	n.envelopeDecay = s.EnvelopeDecay
	n.lengthCounter = s.LengthCounter
	n.timer = s.Timer
	n.period = s.Period
	n.lfsr = s.LFSR
	if n.lfsr == 0 {
		n.lfsr = 1
	}
	n.shortMode = s.ShortMode
}

func (d *dmcChannel) save() DMCState {
	return DMCState{
		Enabled: d.enabled, IRQEnable: d.irqEnable, Loop: d.loop,
		RateIdx: d.rateIdx, Output: d.output,
		SampleAddrBase: d.sampleAddrBase, SampleLenBase: d.sampleLenBase,
		CurrentAddr: d.currentAddr, BytesRemaining: d.bytesRemaining,
		SampleBuffer: d.sampleBuffer, BufferEmpty: d.bufferEmpty,
		ShiftRegister: d.shiftRegister, BitsRemaining: d.bitsRemaining,
		Silenced: d.silenced, Timer: d.timer,
		IRQPending: d.irqPending,
	}
}

func (d *dmcChannel) load(s DMCState) {
	d.enabled = s.Enabled
	d.irqEnable = s.IRQEnable
	d.loop = s.Loop
	d.rateIdx = s.RateIdx
	d.output = s.Output
	d.sampleAddrBase = s.SampleAddrBase
	d.sampleLenBase = s.SampleLenBase
	d.currentAddr = s.CurrentAddr
	d.bytesRemaining = s.BytesRemaining
	d.sampleBuffer = s.SampleBuffer
	d.bufferEmpty = s.BufferEmpty
	d.shiftRegister = s.ShiftRegister
	d.bitsRemaining = s.BitsRemaining
	d.silenced = s.Silenced
	d.timer = s.Timer
	d.irqPending = s.IRQPending
}

var errBadStateSize = errors.New("save-state payload size mismatch")

// keep errBadStateSize used so go vet doesn't grumble; LoadFullState
// signatures all return error so downstream gating can wrap this when
// they encounter a malformed envelope.
var _ = errBadStateSize
