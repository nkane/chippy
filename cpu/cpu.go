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
	// VariantW65816 models the WDC 65C816. On reset it powers up in
	// emulation mode (E=1) where it is 65C02-compatible with 8-bit
	// registers; XCE flips to native mode (E=0) with 16-bit accumulator /
	// index registers (widths gated by the M/X status bits), bank registers
	// (DBR/PBR), a direct-page register (D), and 24-bit addressing. Phase 1
	// (#456) implements the emulation-mode foundation + mode switching; the
	// native 16-bit path is #462.
	VariantW65816
)

func (v Variant) String() string {
	switch v {
	case VariantCMOS65C02:
		return "65c02"
	case VariantNES:
		return "nes"
	case VariantW65816:
		return "65816"
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

	// dmaBus is a cached DmaReadBus assertion on Bus. Set via SetBus().
	// nil when the host bus doesn't model DMA-read open-bus; the DMA
	// loop's 256-read sprite window then pays no per-read type-assert.
	// See dma.go.
	dmaBus DmaReadBus

	// accessHook, when non-nil, is invoked on every bus access with the
	// address + AccessKind (issue #421). nil by default — a host opts in via
	// SetAccessHook. See access.go.
	accessHook func(addr uint16, kind AccessKind)

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

	// 65816 wide state (VariantW65816 only; zero/unused for 6502/CMOS/NES).
	// The 8-bit A/X/Y/SP/P above are the 6502-compatible low-byte view; these
	// carry the high halves + banks. E selects emulation (E=true, 6502-style,
	// 8-bit registers, stack in page $01) vs native (E=false). In native mode
	// P bits 5/4 are M (accumulator width) / X (index width); in emulation
	// they read as the 6502's U/B. Accumulator C = B<<8 | A; index hi = XH/YH;
	// 16-bit SP = SPHi<<8 | SP. (#456)
	B, XH, YH, SPHi byte
	D               uint16 // direct-page register
	DBR, PBR        byte   // data + program bank registers
	E               bool   // emulation mode (true on reset)
	bus24           Bus24  // 24-bit memory for the 65816 core (SetBus24)

	// Per-access pin state for the 65816 bus trace (#495). busPins is the
	// VDA/VPA/VPB/MLB bitfield the active access sets just before read24/
	// write24; busE/busM/busX snapshot the E/M/X status pins at instruction
	// start (they reflect the flags before any mid-instruction change). Read
	// by the bus-trace recorder; functionally inert.
	busPins          byte
	busE, busM, busX bool
	opcodeFetch      bool // the next fetch816 is the opcode (asserts VDA+VPA)

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

	// pendingStall is retained as a v1 save-state field (frozen
	// per docs/state-format.md). #376 retired the Stall + drain
	// path in favor of ProcessPendingDma; this counter is never
	// touched by the live emulator anymore but stays in the JSON
	// schema for backward compat with v1.x save-state files.
	pendingStall int

	// Per-cycle DMA state machine fields (#376, Mesen2 ProcessPendingDma
	// port). The whole DMA loop runs inside CPU.read at opcode-fetch
	// time when needHalt is set. Peripherals (OAMDMA writes to $4014,
	// DMC sample fetches) drive these flags; the CPU drains them.
	//
	// needHalt — pending DMA halt cycle. Set by SetNeedSpriteDma or
	//   SetNeedDmcDma. Cleared when ProcessPendingDma services the halt.
	// spriteDmaTransfer — OAMDMA active. Set by OAMDMA.Write; cleared
	//   when the 256 read/write pairs finish.
	// spriteDmaOffset — high byte of the OAMDMA source page (= $4014 value).
	// dmcDmaRunning — DMC sample fetch active. Set by DMC.maybeRefill
	//   via cpu.SetNeedDmcDma; cleared after the read+SetDmcReadBuffer.
	// abortDmcDma — DMC fetch cancelled (e.g. $4015 disable mid-fetch).
	//   ProcessPendingDma honours this on its next iteration.
	//
	// All fields stay zero on non-NES variants. Wiring lives in the
	// nessy build (OAMDMA.Write + DMC.maybeRefill call SetNeed*).
	needHalt          bool
	spriteDmaTransfer bool
	spriteDmaOffset   byte
	dmcDmaRunning     bool
	abortDmcDma       bool
	// needDummyRead — the extra dummy-read cycle a DMC DMA performs
	//   between the halt cycle and the actual sample read (Mesen
	//   NesCpu::StartDmcTransfer sets it alongside needHalt). Without it
	//   the DMC read (and bytesRemaining decrement) lands one cycle too
	//   early, mis-timing dmc_dma_during_read* (#20-nessy).
	needDummyRead bool

	// dmcFetcher is the APU-side hook ProcessPendingDma calls to
	// look up the DMC's current read address + push the fetched
	// byte back into the sample buffer. Wired via SetDMCFetcher
	// by the nessy NES-emulator consumer; nil for non-NES variants.
	dmcFetcher DMCFetcher
}

// CurrentCycle returns the running CPU cycle counter. Bus-stealing
// peripherals read this to align dummy-cycle penalties against
// even/odd CPU cycle parity.
func (c *CPU) CurrentCycle() uint64 { return c.Cycles }

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

// SetNeedSpriteDma signals that a $4014 OAMDMA write just landed.
// page is the value written to $4014 ($XX → source page $XX00).
// The CPU's ProcessPendingDma loop (#376) consumes this on the
// next opcode fetch. Replaces the OAMDMA-side Stall(513) call
// pattern; peripheral only sets the state, CPU drains it.
func (c *CPU) SetNeedSpriteDma(page byte) {
	c.spriteDmaOffset = page
	c.spriteDmaTransfer = true
	c.needHalt = true
}

// SetNeedDmcDma signals that the DMC needs a sample byte. Called
// from dmcChannel.maybeRefill when its sample buffer is empty +
// bytes-remaining > 0. The CPU's ProcessPendingDma loop fetches
// the byte (via APU.GetDmcReadAddress) and pushes it back through
// APU.SetDmcReadBuffer.
func (c *CPU) SetNeedDmcDma() {
	c.dmcDmaRunning = true
	c.needDummyRead = true
	c.needHalt = true
}

// AbortDmcDma cancels an in-flight DMC fetch (e.g. $4015 disable
// or $4010 IRQ-off mid-DMA). ProcessPendingDma honours the flag
// on its next iteration, dropping dmcDmaRunning before issuing
// the read.
func (c *CPU) AbortDmcDma() { c.abortDmcDma = true }

// NeedHalt reports whether a DMA halt cycle is pending. Exposed
// for #376 tests + the future ProcessPendingDma hot path.
func (c *CPU) NeedHalt() bool { return c.needHalt }

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
	if d, ok := b.(DmaReadBus); ok {
		c.dmaBus = d
	} else {
		c.dmaBus = nil
	}
	c.nesCycle = c.Variant == VariantNES && c.busTicker != nil
}

func (c *CPU) bindTable() {
	switch c.Variant {
	case VariantCMOS65C02:
		c.opcodes = &OpcodesCMOS
	case VariantW65816:
		c.opcodes = &Opcodes65816
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
	if c.Variant == VariantW65816 {
		// 65816 powers up in emulation mode (E=1): 8-bit registers, stack in
		// page $01, M/X forced 1. Native-only state clears (#456).
		c.E = true
		c.B, c.XH, c.YH = 0, 0, 0
		c.SPHi = 0x01
		c.D = 0
		c.DBR, c.PBR = 0, 0
	}
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
// without a bus access. Used by Reset's 8-cycle warmup pass on
// VariantNES to seed the APU's $4017 reset-delay state. The DMA
// path uses dmaStartCycle / dmaEndCycle instead (#376).
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
// read performs a data read (AccessRead). fetch performs an opcode fetch
// (AccessExec). Both route through busRead so the per-byte access hook (#421)
// sees the right kind; the hook is nil by default, so this is a single
// not-taken branch on the hot path when no debugger is attached.
func (c *CPU) read(addr uint16) byte  { return c.busRead(addr, AccessRead) }
func (c *CPU) fetch(addr uint16) byte { return c.busRead(addr, AccessExec) }

func (c *CPU) busRead(addr uint16, kind AccessKind) byte {
	if c.nesCycle {
		// Drain any pending DMA window before the actual read so the
		// bus-steal cycles land on the right side of this read in
		// the cycle stream (Mesen NesCpu::Read #376).
		if c.needHalt {
			c.ProcessPendingDma(addr)
		}
		c.instrCycles++
		c.masterClock += cpuStartReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.busTicker.Tick(1)
		v := c.Bus.Read(addr)
		if c.accessHook != nil {
			c.accessHook(addr, kind)
		}
		c.masterClock += cpuEndReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.sampleNMI()
		return v
	}
	v := c.Bus.Read(addr)
	if c.accessHook != nil {
		c.accessHook(addr, kind)
	}
	return v
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
		if c.accessHook != nil {
			c.accessHook(addr, AccessWrite)
		}
		c.masterClock += cpuEndWriteShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.sampleNMI()
		return
	}
	c.Bus.Write(addr, v)
	if c.accessHook != nil {
		c.accessHook(addr, AccessWrite)
	}
}

// idle burns one cycle the way the 6502 does on internal / fix-up /
// dummy cycles: a real (discarded) bus read at addr so MMIO side
// effects are faithful. NES path uses the same pre/post-bus PPU split
// as a real read.
func (c *CPU) idle(addr uint16) {
	if c.nesCycle {
		// A pending DMA halt drains before this cycle's bus access, same
		// as busRead — Mesen polls ProcessPendingDma on every CPU cycle,
		// including dummy/idle reads. Without this, a halt armed going
		// into an idle cycle (e.g. a taken branch's dummy read) is missed
		// here and only drained at the next real read — landing the DMC
		// steal one cycle late (wrong get/put parity → 3-cycle steal where
		// hardware/Mesen take 4). Broke dma_2007_read's phase drift (#493).
		if c.needHalt {
			c.ProcessPendingDma(addr)
		}
		c.instrCycles++
		c.masterClock += cpuStartReadShift
		if c.ppuRunner != nil {
			c.ppuRunner.Run(c.masterClock - cpuPPUOffset)
		}
		c.busTicker.Tick(1)
		_ = c.Bus.Read(addr)
		if c.accessHook != nil {
			c.accessHook(addr, AccessRead)
		}
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
