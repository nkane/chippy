package cpu

// Addressing modes.
type AddrMode int

const (
	IMP AddrMode = iota // implied
	ACC                 // accumulator
	IMM                 // immediate
	ZP                  // zero page
	ZPX                 // zero page,X
	ZPY                 // zero page,Y
	REL                 // relative
	ABS                 // absolute
	ABX                 // absolute,X
	ABY                 // absolute,Y
	IND                 // (indirect)
	IZX                 // (indirect,X)
	IZY                 // (indirect),Y
)

// resolve fetches operand address (and pageCrossed) for a given mode.
// PC is left pointing past the opcode byte; this advances PC past operand bytes.
func (c *CPU) resolve(m AddrMode) (addr uint16, pageCrossed bool) {
	switch m {
	case IMP, ACC:
		return 0, false
	case IMM:
		addr = c.PC
		c.PC++
	case ZP:
		addr = uint16(c.Bus.Read(c.PC))
		c.PC++
	case ZPX:
		addr = uint16(c.Bus.Read(c.PC)+c.X) & 0xFF
		c.PC++
	case ZPY:
		addr = uint16(c.Bus.Read(c.PC)+c.Y) & 0xFF
		c.PC++
	case REL:
		off := int8(c.Bus.Read(c.PC))
		c.PC++
		addr = uint16(int32(c.PC) + int32(off))
	case ABS:
		lo := uint16(c.Bus.Read(c.PC))
		hi := uint16(c.Bus.Read(c.PC + 1))
		c.PC += 2
		addr = lo | hi<<8
	case ABX:
		lo := uint16(c.Bus.Read(c.PC))
		hi := uint16(c.Bus.Read(c.PC + 1))
		c.PC += 2
		base := lo | hi<<8
		addr = base + uint16(c.X)
		pageCrossed = (base & 0xFF00) != (addr & 0xFF00)
	case ABY:
		lo := uint16(c.Bus.Read(c.PC))
		hi := uint16(c.Bus.Read(c.PC + 1))
		c.PC += 2
		base := lo | hi<<8
		addr = base + uint16(c.Y)
		pageCrossed = (base & 0xFF00) != (addr & 0xFF00)
	case IND:
		lo := uint16(c.Bus.Read(c.PC))
		hi := uint16(c.Bus.Read(c.PC + 1))
		c.PC += 2
		ptr := lo | hi<<8
		// 6502 page-wrap bug
		loAddr := uint16(c.Bus.Read(ptr))
		hiAddr := uint16(c.Bus.Read((ptr & 0xFF00) | uint16(byte(ptr)+1)))
		addr = loAddr | hiAddr<<8
	case IZX:
		zp := c.Bus.Read(c.PC) + c.X
		c.PC++
		lo := uint16(c.Bus.Read(uint16(zp)))
		hi := uint16(c.Bus.Read(uint16(zp + 1)))
		addr = lo | hi<<8
	case IZY:
		zp := c.Bus.Read(c.PC)
		c.PC++
		lo := uint16(c.Bus.Read(uint16(zp)))
		hi := uint16(c.Bus.Read(uint16(zp + 1)))
		base := lo | hi<<8
		addr = base + uint16(c.Y)
		pageCrossed = (base & 0xFF00) != (addr & 0xFF00)
	}
	return
}
