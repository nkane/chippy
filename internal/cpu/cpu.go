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

// CPU is an NMOS 6502.
type CPU struct {
	A, X, Y, SP, P byte
	PC             uint16
	Cycles         uint64
	Bus            Bus

	// Debug helpers
	Halted bool
}

func New(bus Bus) *CPU {
	c := &CPU{Bus: bus}
	c.Reset()
	return c
}

func (c *CPU) Reset() {
	c.A, c.X, c.Y = 0, 0, 0
	c.SP = 0xFD
	c.P = FlagU | FlagI
	lo := uint16(c.Bus.Read(VecReset))
	hi := uint16(c.Bus.Read(VecReset + 1))
	c.PC = lo | hi<<8
	c.Cycles = 7
	c.Halted = false
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
func (c *CPU) NMI() {
	c.push16(c.PC)
	c.push((c.P | FlagU) &^ FlagB)
	c.setFlag(FlagI, true)
	lo := uint16(c.Bus.Read(VecNMI))
	hi := uint16(c.Bus.Read(VecNMI + 1))
	c.PC = lo | hi<<8
	c.Cycles += 7
}

func (c *CPU) IRQ() {
	if c.hasFlag(FlagI) {
		return
	}
	c.push16(c.PC)
	c.push((c.P | FlagU) &^ FlagB)
	c.setFlag(FlagI, true)
	lo := uint16(c.Bus.Read(VecIRQ))
	hi := uint16(c.Bus.Read(VecIRQ + 1))
	c.PC = lo | hi<<8
	c.Cycles += 7
}
