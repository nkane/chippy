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
)

func (v Variant) String() string {
	switch v {
	case VariantCMOS65C02:
		return "65c02"
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
	Variant   Variant

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
}

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
func (c *CPU) push(v byte) {
	c.Bus.Write(0x100|uint16(c.SP), v)
	c.SP--
}
func (c *CPU) pop() byte {
	c.SP++
	return c.Bus.Read(0x100 | uint16(c.SP))
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

// AssertIRQ raises the IRQ line. While asserted and FlagI is clear, the
// interrupt is taken at the end of every instruction. Peripherals call
// AssertIRQ when their condition is active and ReleaseIRQ when cleared.
func (c *CPU) AssertIRQ()  { c.irqLine = true }
func (c *CPU) ReleaseIRQ() { c.irqLine = false }

// IRQAsserted reports whether the IRQ line is currently held.
func (c *CPU) IRQAsserted() bool { return c.irqLine }

// TriggerNMI latches a non-maskable interrupt. Edge-triggered: the
// dispatcher takes the interrupt once, then clears the latch. Calling
// TriggerNMI repeatedly while one is already pending coalesces into a
// single NMI.
func (c *CPU) TriggerNMI() { c.nmiPending = true }

// serviceNMI performs the 7-cycle NMI vector dispatch.
func (c *CPU) serviceNMI() {
	c.push16(c.PC)
	c.push((c.P | FlagU) &^ FlagB)
	c.setFlag(FlagI, true)
	// CMOS quirk: interrupt entry also clears D so handlers can rely
	// on binary mode. NMOS leaves D alone.
	if c.Variant == VariantCMOS65C02 {
		c.setFlag(FlagD, false)
	}
	lo := uint16(c.Bus.Read(VecNMI))
	hi := uint16(c.Bus.Read(VecNMI + 1))
	c.PC = lo | hi<<8
	c.Cycles += 7
	c.nmiPending = false
}

// serviceIRQ performs the 7-cycle IRQ vector dispatch. Caller must have
// already verified FlagI is clear.
func (c *CPU) serviceIRQ() {
	c.push16(c.PC)
	c.push((c.P | FlagU) &^ FlagB)
	c.setFlag(FlagI, true)
	if c.Variant == VariantCMOS65C02 {
		c.setFlag(FlagD, false)
	}
	lo := uint16(c.Bus.Read(VecIRQ))
	hi := uint16(c.Bus.Read(VecIRQ + 1))
	c.PC = lo | hi<<8
	c.Cycles += 7
}
