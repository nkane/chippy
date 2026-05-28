package cpu

// Status flag bits.
const (
	FlagC byte = 1 << 0 // carry
	FlagZ byte = 1 << 1 // zero
	FlagI byte = 1 << 2 // interrupt disable
	FlagD byte = 1 << 3 // decimal
	FlagB byte = 1 << 4 // break
	FlagU byte = 1 << 5 // unused (always 1)
	FlagV byte = 1 << 6 // overflow
	FlagN byte = 1 << 7 // negative
)

// Vectors.
const (
	VecNMI   uint16 = 0xFFFA
	VecReset uint16 = 0xFFFC
	VecIRQ   uint16 = 0xFFFE
)

// Variant selects which opcode table the CPU executes.
type Variant int

const (
	VariantNMOS      Variant = iota // MOS 6502 (default)
	VariantCMOS65C02                // WDC/Rockwell 65C02
	// VariantNES models the Ricoh 2A03 found in the NES — an NMOS 6502
	// with decimal-mode arithmetic disabled (the FlagD bit still
	// toggles via SED/CLD/PHP/PLP so programs can probe it, but
	// ADC/SBC under D=1 behave as if D were clear). All other behavior
	// matches NMOS, including the JMP-indirect page-wrap bug.
	VariantNES
)

func (v Variant) String() string {
	switch v {
	case VariantCMOS65C02:
		return "65c02"
	case VariantNES:
		return "nes"
	default:
		return "nmos"
	}
}

// CPU is a 6502-family processor (NMOS or CMOS 65C02 per Variant).
type CPU struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Bus            Bus
	// busTicker is a cached type assertion on Bus. Set via SetBus().
	// Step() checks this instead of doing a per-call type assertion;
	// the no-ticker fast path stays single-digit-ns.
	busTicker Ticker

	// ppuRunner drives PPU dot advance with master-clock deadlines
	// (#372 redesign). Set via SetPPURunner after the PPU is built.
	// When non-nil, read/write/idle split the cycle's master-clock
	// budget around the bus access and call Run at each split so the
	// PPU runs in lockstep with the CPU's mid-cycle phase, matching
	// Mesen2 NesCpu::Start/EndCpuCycle.
	ppuRunner PPURunner

	// masterClock is the CPU's running master-clock counter (NTSC: 12
	// mc per CPU cycle, 4 mc per PPU dot). Bus reads add startClock-1
	// (5 mc) before the access and endClock+1 (7 mc) after; writes
	// swap to 7+5. Reset seeds it to cpuDivider + cpuOffset (12+0).
	masterClock uint64

	Variant Variant

	// Optional per-instruction execution hook. When non-nil, Step() invokes
	// Tracer.LogStep at the instruction boundary just before opcode fetch.
	// Implementations must be cheap on the disabled path.
	Tracer Tracer

	// Debug helpers
	Halted bool

	// stoppedBySTP marks a CMOS STP-induced halt. STP halts until Reset —
	// unlike a self-jump halt (clearable by any interrupt) or a WAI halt
	// (clearable by IRQ/NMI), so Step() refuses to service interrupts while
	// this flag is set.
	stoppedBySTP bool

	// extraCycles is set by handlers (e.g. taken branches) to add to the
	// instruction's base cycle count for the current Step. Reset each Step.
	extraCycles int

	// opcodes is the active opcode table; chosen from Variant in New/Reset.
	opcodes *[256]Instr

	// Interrupt lines.
	//   - irqLine: level-triggered. While true and FlagI is clear, IRQ
	//     fires at every instruction boundary.
	//   - nmiPending: edge-triggered latch. TriggerNMI sets it; the
	//     dispatcher clears it after servicing. Holding the line asserted
	//     does NOT cause repeated NMIs — the caller must call TriggerNMI
	//     again (matching real silicon's edge sensitivity).
	irqLine    bool
	nmiPending bool

	// nmiDue is the NES-variant interrupt-poll latch (#342). The 6502
	// samples the NMI line before an instruction's final cycle, so an
	// edge asserted on that last cycle is recognised one instruction
	// later. With the per-cycle interleave, every tick advances the poll
	// by one cycle via nmiPollPrev (a 1-cycle delay), so nmiDue ends an
	// instruction holding the line state as of its penultimate cycle.
	// The *next* Step services it. NMOS/CMOS keep the immediate path.
	nmiDue      bool
	nmiPollPrev bool

	// irqDue is the IRQ analogue of nmiDue (#369). IRQ is level + mask:
	// (irqLine AND !FlagI) sampled at the penultimate cycle. CLI/SEI/PLP
	// change FlagI in their final cycle — after the poll — so the new
	// mask state only affects interrupts one instruction later, which
	// cpu_interrupts_v2 cli_latency pins.
	irqDue      bool
	irqPollPrev bool

	// /NMI level model (#342). The PPU drives nmiLineLevel via SetNMILine
	// (= vblank-flag AND PPUCTRL.7). The CPU edge-detects a low→asserted
	// transition into nmiPending after each cycle's bus op (sampleNMI),
	// so a $2002 read that drops the line in the same cycle it would rise
	// is never latched — the 2C02 NMI suppression race, for free.
	nmiLineLevel bool
	nmiLinePrev  bool

	// Per-cycle interleave state (#342, VariantNES). nesCycle caches
	// "Variant==NES && a bus ticker is wired"; when set, each bus access
	// ticks the whole chain (PPU/APU/cart) one cycle *before* the access
	// so a $2002 read samples the PPU at its true dot and the /NMI race
	// interleaves. instrCycles counts the ticks issued this instruction;
	// Step asserts it equals the instruction's accounted cycle total.
	nesCycle    bool
	instrCycles int

	// irqSources holds the set of named IRQ sources currently asserting
	// the line. The CPU sees a wired-OR — irqLine is true iff the set
	// is non-empty. Populated lazily by AssertIRQSource so a CPU that
	// never receives a tagged-source call has no map overhead.
	irqSources map[string]struct{}

	// pendingStall is a cycle debt owed to a peripheral that took over
	// the bus (e.g. $4014 OAMDMA). The next Step() drains the whole
	// counter as one block — bus ticks fire, c.Cycles advances, no
	// opcode executes. See Stall().
	pendingStall int

	// stallStepper runs one DMA-state-machine step per stall cycle so
	// the peripheral can spread its bus reads/writes across the stall
	// window (#372 test 4 irq_and_dma). nil for non-NES variants and
	// for pre-OAMDMA wiring.
	stallStepper StallStepper
}

// Stall queues N cycles of CPU stall. The very next Step() consumes
// the whole queue as a single non-opcode unit: the bus ticker (PPU /
// APU) sees the cycle delta, Cycles advances, no opcode fetches.
//
// Designed for bus-stealing peripherals — OAMDMA writes 256 bytes and
// reports its 513-cycle stall via this hook. Multiple calls accumulate.
func (c *CPU) Stall(cycles int) {
	if cycles > 0 {
		c.pendingStall += cycles
	}
}

// CurrentCycle returns the running CPU cycle counter. Bus-stealing
// peripherals (OAMDMA, DMC sample fetch) read this to align their
// dummy-cycle penalties against odd vs even CPU cycles.
func (c *CPU) CurrentCycle() uint64 { return c.Cycles }

// PendingStall returns the un-drained stall debt queued by an earlier
// peripheral (typically OAMDMA). A non-zero value at DMC-fetch time
// signals bus contention — the DMC fetch lands inside the OAMDMA
// window and pays a 2-cycle alignment penalty per nesdev (#300).
func (c *CPU) PendingStall() int { return c.pendingStall }

func New(bus Bus) *CPU {
	return NewVariant(bus, VariantNMOS)
}

// NewVariant constructs a CPU with the given variant.
func NewVariant(bus Bus, v Variant) *CPU {
	c := &CPU{Variant: v}
	c.SetBus(bus)
	c.bindTable()
	c.Reset()
	return c
}

// SetStallStepper wires the per-cycle DMA hook called during stall
// drains (#372 test 4). nil resets to "no per-cycle action" (legacy
// behavior — peripheral did all bus reads upfront via batch).
func (c *CPU) SetStallStepper(s StallStepper) { c.stallStepper = s }

// SetPPURunner wires the PPU master-clock-deadline hook. After this
// call the NES variant's read/write/idle paths split the cycle's
// master-clock budget around the bus access and call Run at each split.
// Callers must also flip the PPU's cpuDriven flag (so MMIO's Ticker
// fan-out stops double-advancing); see ppu.SetCPUDriven.
func (c *CPU) SetPPURunner(r PPURunner) { c.ppuRunner = r }

// SetBus swaps the CPU's bus and refreshes the cached busTicker
// (#175). Callers must use this rather than assigning c.Bus directly
// so the per-Step ticker dispatch stays correct after a bus wrap.
func (c *CPU) SetBus(b Bus) {
	c.Bus = b
	if t, ok := b.(Ticker); ok {
		c.busTicker = t
	} else {
		c.busTicker = nil
	}
	c.nesCycle = c.Variant == VariantNES && c.busTicker != nil
}

func (c *CPU) bindTable() {
	switch c.Variant {
	case VariantCMOS65C02:
		c.opcodes = &OpcodesCMOS
	default:
		c.opcodes = &Opcodes
	}
}

func (c *CPU) Reset() {
	if c.opcodes == nil {
		c.bindTable()
	}
	c.A, c.X, c.Y = 0, 0, 0
	c.SP = 0xFD
	c.P = FlagU | FlagI
	lo := uint16(c.Bus.Read(VecReset))
	hi := uint16(c.Bus.Read(VecReset + 1))
	c.PC = lo | hi<<8
	c.Cycles = 7
	c.Halted = false
	c.stoppedBySTP = false
	// Mesen2 NesCpu::Reset master-clock seeding + 8-cycle reset loop.
	// masterClock starts at cpuDivider + cpuOffset (= 12 + 0 = 12) per
	// the NTSC default; then 8 read-flavor cycles each add 12 mc, with
	// PPU.Run firing at the Start (mc - ppuOffset) and End deadlines.
	// Total post-reset masterClock = 12 + 8*12 = 108, PPU at 26 dots
	// when the first instruction runs — matching Mesen "9 to 12 clocks
	// before first instruction begins" for the phantom $4017=$00 APU
	// frame-counter reset.
	// Skip the 8-cycle reset advance unless ppuRunner is wired —
	// otherwise NewVariant's pre-PPU Reset double-advances the APU
	// (chippy calls Reset twice: once from NewVariant before the PPU
	// is registered, once from wiring after). The post-wiring Reset
	// is the one that needs to match Mesen2's reset loop; the pre-
	// PPU one only needs to load the vector and leave clean state.
	if c.Variant == VariantNES && c.busTicker != nil && c.ppuRunner != nil {
		c.masterClock = cpuDivider
		for range 8 {
			c.stallTick()
		}
	}
}

// flag helpers
func (c *CPU) setFlag(f byte, on bool) {
	if on {
		c.P |= f
	} else {
		c.P &^= f
	}
}
func (c *CPU) hasFlag(f byte) bool { return c.P&f != 0 }

func (c *CPU) setZN(v byte) {
	c.setFlag(FlagZ, v == 0)
	c.setFlag(FlagN, v&0x80 != 0)
}

// stack

// stallTick advances master-clock + PPU + APU by one full CPU cycle
// without a bus access. Used by the OAMDMA stall drain — the bus is
// stolen by the peripheral so the CPU sees an opaque "wait one cycle"
// per stall unit, but the PPU/APU still need to advance + interrupt
// poll latches still advance with each cycle (#372 test 4 needs this
// to detect IRQ assertions that happen mid-DMA at the right cycle).
func (c *CPU) stallTick() {
	c.instrCycles++
	c.masterClock += cpuDivider
	if c.ppuRunner != nil {
		c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
	}
	c.busTicker.Tick(1)
	c.sampleNMI()
}

// sampleNMI runs at the end of a cycle, after the bus op. It edge-detects
// the /NMI line into nmiPending, then advances the penultimate-poll latch
// (nmiDue) with a one-cycle delay so that at an instruction's last cycle
// nmiDue holds the pending state as of its penultimate cycle. Because the
// edge is sampled *after* the bus op, a $2002 read that drops the line in
// the same cycle it rose leaves no rising edge — the suppression race.
func (c *CPU) sampleNMI() {
	if c.nmiLineLevel && !c.nmiLinePrev {
		c.nmiPending = true
	}
	c.nmiLinePrev = c.nmiLineLevel
	c.nmiDue = c.nmiPollPrev
	c.nmiPollPrev = c.nmiPending
	c.irqDue = c.irqPollPrev
	c.irqPollPrev = c.irqLine && !c.hasFlag(FlagI)
}

// SetNMILine sets the /NMI line level the CPU sees. The NES PPU drives it
// from (vblank-flag AND PPUCTRL.7); the CPU edge-detects in sampleNMI.
func (c *CPU) SetNMILine(level bool) { c.nmiLineLevel = level }

// NES per-cycle master-clock split (Mesen2 NesCpu::Start/EndCpuCycle).
// NTSC default: 12 master clocks per CPU cycle, 4 per PPU dot. Reads
// commit at master-clock + (startClock-1) = +5; the EndCpuCycle adds
// (endClock+1) = +7 for a 12-mc total. Writes swap: +7 pre-access,
// +5 post-access — modelling the 6502's φ1 (writes) vs φ2 (reads)
// phase. ppuOffset shifts PPU's masterClock 1 mc behind the CPU's so
// it lags the right fraction of a dot relative to the bus.
const (
	cpuStartClock      = 6
	cpuEndClock        = 6
	cpuStartReadShift  = cpuStartClock - 1 // 5 mc pre-read
	cpuEndReadShift    = cpuEndClock + 1   // 7 mc post-read
	cpuStartWriteShift = cpuStartClock + 1 // 7 mc pre-write
	cpuEndWriteShift   = cpuEndClock - 1   // 5 mc post-write
	cpuDivider         = 12                // total mc per CPU cycle
	cpuPPUOffset       = 1                 // PPU lag (Mesen default, _ppuOffset=1)
)

// read / write perform one bus cycle. NES path splits the master-clock
// budget around the bus access and runs PPU at each split via the
// PPURunner deadline, matching Mesen2 NesCpu::Start/EndCpuCycle. APU +
// cart advance 1 CPU cycle via busTicker.Tick(1) at the StartCpuCycle
// equivalent point (after PPU pre-advance, before bus access). PPU.Tick
// is a no-op when SetCPUDriven(true) lands so MMIO's fan-out doesn't
// double-advance.
func (c *CPU) read(addr uint16) byte {
	if c.nesCycle {
		c.instrCycles++
		c.masterClock += cpuStartReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.busTicker.Tick(1)
		v := c.Bus.Read(addr)
		c.masterClock += cpuEndReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.sampleNMI()
		return v
	}
	return c.Bus.Read(addr)
}

func (c *CPU) write(addr uint16, v byte) {
	if c.nesCycle {
		c.instrCycles++
		c.masterClock += cpuStartWriteShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.busTicker.Tick(1)
		c.Bus.Write(addr, v)
		c.masterClock += cpuEndWriteShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.sampleNMI()
		return
	}
	c.Bus.Write(addr, v)
}

// idle burns one cycle the way the 6502 does on internal / fix-up /
// dummy cycles: a real (discarded) bus read at addr so MMIO side
// effects are faithful. NES path uses the same pre/post-bus PPU split
// as a real read.
func (c *CPU) idle(addr uint16) {
	if c.nesCycle {
		c.instrCycles++
		c.masterClock += cpuStartReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.busTicker.Tick(1)
		_ = c.Bus.Read(addr)
		c.masterClock += cpuEndReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.sampleNMI()
	}
}

func (c *CPU) push(v byte) {
	c.write(0x100|uint16(c.SP), v)
	c.SP--
}
func (c *CPU) pop() byte {
	c.SP++
	return c.read(0x100 | uint16(c.SP))
}
func (c *CPU) push16(v uint16) {
	c.push(byte(v >> 8))
	c.push(byte(v))
}
func (c *CPU) pop16() uint16 {
	lo := uint16(c.pop())
	hi := uint16(c.pop())
	return lo | hi<<8
}

// IRQ / NMI

// AssertIRQ raises the anonymous IRQ source. Equivalent to
// AssertIRQSource("") — kept for backward compat with single-source
// tests that predate the named-source surface (#247).
func (c *CPU) AssertIRQ() { c.AssertIRQSource("") }

// ReleaseIRQ clears the anonymous IRQ source. Equivalent to
// ClearIRQSource("").
func (c *CPU) ReleaseIRQ() { c.ClearIRQSource("") }

// AssertIRQSource raises the named IRQ source. Real NES silicon has
// up to three concurrent sources (APU frame counter, APU DMC,
// MMC3-style scanline IRQ); the CPU sees a wired-OR of every active
// source. While *any* source is asserted and FlagI is clear, the
// interrupt is taken at the next instruction boundary.
//
// Peripherals call AssertIRQSource when their condition becomes
// active and ClearIRQSource when it goes away — symmetry matters,
// since the line is level-sensitive (asserted-until-cleared).
func (c *CPU) AssertIRQSource(src string) {
	if c.irqSources == nil {
		c.irqSources = map[string]struct{}{}
	}
	c.irqSources[src] = struct{}{}
	c.irqLine = true
}

// ClearIRQSource removes the named source. The IRQ line goes low
// only when every asserted source has been cleared.
func (c *CPU) ClearIRQSource(src string) {
	delete(c.irqSources, src)
	c.irqLine = len(c.irqSources) > 0
}

// IRQAsserted reports whether the IRQ line is currently held by any
// source.
func (c *CPU) IRQAsserted() bool { return c.irqLine }

// TriggerNMI latches a non-maskable interrupt. Edge-triggered: the
// dispatcher takes the interrupt once, then clears the latch. Calling
// TriggerNMI repeatedly while one is already pending coalesces into a
// single NMI.
func (c *CPU) TriggerNMI() { c.nmiPending = true }

// serviceNMI performs the 7-cycle NMI vector dispatch. The two leading
// idle cycles + the ticked pushes / vector reads keep the PPU in lockstep
// through interrupt entry on the NES variant (#342); other variants take
// the no-op idle path and the post-instruction batch tick.
func (c *CPU) serviceNMI() {
	// Clear the edge latch *before* the 7 service cycles so the sampleNMI
	// calls inside serviceVector don't re-arm nmiDue from the still-
	// pending edge and trigger a spurious second NMI (#342). The line
	// itself may stay asserted (flag + bit 7); nmiLinePrev holds, so no
	// new edge fires until the line drops and rises again.
	c.nmiPending = false
	c.serviceVector(VecNMI)
}

// serviceVector runs the shared 7-cycle interrupt dispatch (NMI/IRQ).
// For the per-cycle NES path it ticks all seven cycles so the PPU stays
// in lockstep through interrupt entry (#342); other variants take the
// no-op idle path and the post-instruction batch tick.
func (c *CPU) serviceVector(vec uint16) {
	c.idle(c.PC) // two internal cycles
	c.idle(c.PC)
	c.push16(c.PC)
	// Vector hijacking (#369, redone for #372 to match Mesen2 NesCpu::IRQ
	// ordering): the NMI-pending check happens BETWEEN push16(PC) and
	// push(P). Mesen sets P with I=1 only on the NMI-side branch's P
	// push, so if NMI hijacks, the pushed P captures the post-handler
	// I=1 state on subsequent inheritance — and importantly the check
	// fires 1 CPU cycle earlier than chippy's previous "after push P"
	// placement, matching the cycle at which Mesen's _needNmi sample
	// catches the late vblank-set transition.
	if c.Variant == VariantNES && vec == VecIRQ && c.nmiPending {
		vec = VecNMI
		c.nmiPending = false
		c.nmiDue = false
		c.nmiPollPrev = false
	}
	c.push((c.P | FlagU) &^ FlagB)
	c.setFlag(FlagI, true)
	// CMOS quirk: interrupt entry also clears D so handlers can rely
	// on binary mode. NMOS leaves D alone.
	if c.Variant == VariantCMOS65C02 {
		c.setFlag(FlagD, false)
	}
	lo := uint16(c.read(vec))
	hi := uint16(c.read(vec + 1))
	c.PC = lo | hi<<8
	c.Cycles += 7
	// 6502 quirk: interrupt sequences don't poll on the handler's first
	// instruction — that instruction must execute before a higher-
	// priority interrupt can preempt (#369, cpu_interrupts_v2). Clear
	// the poll latches; nmiPending stays (the line/edge persists) and
	// the next instruction's poll re-establishes nmiDue, so the NMI
	// fires after that first instruction, not before.
	c.nmiDue = false
	c.nmiPollPrev = false
}

// serviceIRQ performs the 7-cycle IRQ vector dispatch. Caller must have
// already verified FlagI is clear.
func (c *CPU) serviceIRQ() {
	c.serviceVector(VecIRQ)
}
