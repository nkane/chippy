package cpu

// Addressing modes.
type AddrMode int

const (
	IMP    AddrMode = iota // implied
	ACC                    // accumulator
	IMM                    // immediate
	ZP                     // zero page
	ZPX                    // zero page,X
	ZPY                    // zero page,Y
	REL                    // relative
	ABS                    // absolute
	ABX                    // absolute,X
	ABY                    // absolute,Y
	IND                    // (indirect)
	IZX                    // (indirect,X)
	IZY                    // (indirect),Y
	IZP                    // (zero page) — 65C02
	IAX                    // (absolute,X) — 65C02 JMP
	ZPR                    // zp, rel — 65C02 BBR/BBS (3 bytes)
	JSRABS                 // absolute, JSR-only: high operand byte fetched after the stack pushes (#428)
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
		addr = uint16(c.read(c.PC))
		c.PC++
	case ZPX:
		base := c.read(c.PC)
		c.PC++
		c.idle(uint16(base)) // dummy read while adding X
		addr = uint16(base+c.X) & 0xFF
	case ZPY:
		base := c.read(c.PC)
		c.PC++
		c.idle(uint16(base)) // dummy read while adding Y
		addr = uint16(base+c.Y) & 0xFF
	case REL:
		off := int8(c.read(c.PC))
		c.PC++
		addr = uint16(int32(c.PC) + int32(off))
	case ABS:
		lo := uint16(c.read(c.PC))
		hi := uint16(c.read(c.PC + 1))
		c.PC += 2
		addr = lo | hi<<8
	case JSRABS:
		// JSR fetches the low operand byte (cycle 2) but defers the high
		// byte until after the return address is pushed (cycle 6). Read only
		// the low byte here and leave PC at the high byte; opJSR reads it.
		// addr carries the latched low byte.
		addr = uint16(c.read(c.PC))
		c.PC++
	case ABX:
		lo := uint16(c.read(c.PC))
		hi := uint16(c.read(c.PC + 1))
		c.PC += 2
		base := lo | hi<<8
		addr = base + uint16(c.X)
		pageCrossed = (base & 0xFF00) != (addr & 0xFF00)
	case ABY:
		lo := uint16(c.read(c.PC))
		hi := uint16(c.read(c.PC + 1))
		c.PC += 2
		base := lo | hi<<8
		addr = base + uint16(c.Y)
		pageCrossed = (base & 0xFF00) != (addr & 0xFF00)
	case IND:
		lo := uint16(c.read(c.PC))
		hi := uint16(c.read(c.PC + 1))
		c.PC += 2
		ptr := lo | hi<<8
		var loAddr, hiAddr uint16
		if c.Variant == VariantCMOS65C02 {
			// CMOS fixed the page-wrap bug.
			loAddr = uint16(c.read(ptr))
			hiAddr = uint16(c.read(ptr + 1))
		} else {
			// NMOS 6502 page-wrap bug.
			loAddr = uint16(c.read(ptr))
			hiAddr = uint16(c.read((ptr & 0xFF00) | uint16(byte(ptr)+1)))
		}
		addr = loAddr | hiAddr<<8
	case IZX:
		ptr := c.read(c.PC)
		c.PC++
		c.idle(uint16(ptr)) // dummy read while adding X to the pointer
		zp := ptr + c.X
		lo := uint16(c.read(uint16(zp)))
		hi := uint16(c.read(uint16(zp + 1)))
		addr = lo | hi<<8
	case IZY:
		zp := c.read(c.PC)
		c.PC++
		lo := uint16(c.read(uint16(zp)))
		hi := uint16(c.read(uint16(zp + 1)))
		base := lo | hi<<8
		addr = base + uint16(c.Y)
		pageCrossed = (base & 0xFF00) != (addr & 0xFF00)
	case IZP:
		// 65C02: (zp) — like IZY/IZX but no register offset.
		zp := c.read(c.PC)
		c.PC++
		lo := uint16(c.read(uint16(zp)))
		hi := uint16(c.read(uint16(zp + 1)))
		addr = lo | hi<<8
	case IAX:
		// 65C02: (abs,X) — used by JMP. No page-wrap bug.
		lo := uint16(c.read(c.PC))
		hi := uint16(c.read(c.PC + 1))
		c.PC += 2
		ptr := (lo | hi<<8) + uint16(c.X)
		loAddr := uint16(c.read(ptr))
		hiAddr := uint16(c.read(ptr + 1))
		addr = loAddr | hiAddr<<8
	case ZPR:
		// 65C02: BBR/BBS — opcode + zp byte + rel byte. We resolve the
		// branch target here; the bit-test address lives at zp.
		// The handler itself reads c.Bus at zp; we encode zp in the low
		// byte of addr and the target PC in... no — we need both. Simpler:
		// the handler uses two reads off PC directly. Resolve consumes
		// both bytes and returns target; handler peeks back -2 for zp.
		// To keep handler-side simple, we use c.PC pre-resolve sentinel:
		// Actually easiest: don't use ZPR through resolve; let the handler
		// read directly. Mark addr=0 here and have handler do its own
		// fetch using c.PC.
		// We DO need to advance PC by 2 (zp byte + rel byte) so Step
		// gets the right post-instruction PC for branch math.
		// But the handler needs the original PC to read zp/rel. So leave
		// PC alone and let the handler advance it.
		return 0, false
	}
	return
}
