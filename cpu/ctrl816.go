package cpu

// 65816 stack, read-modify-write, and control-flow opcodes (#456 chunk 3).
//
// Stack accesses go through the 24-bit bus at SP16. In emulation mode the
// stack is locked to page 1 (SPHi=$01) and the pointer wraps within the page;
// in native mode SP is a full 16-bit bank-0 pointer.

func (c *CPU) spPush8(v byte) {
	c.pinData()
	c.write24(uint32(c.SP16()), v)
	if c.E {
		c.SP--
	} else {
		c.setSP16(c.SP16() - 1)
	}
}

func (c *CPU) spPull8() byte {
	if c.E {
		c.SP++
	} else {
		c.setSP16(c.SP16() + 1)
	}
	c.pinData()
	return c.read24(uint32(c.SP16()))
}

func (c *CPU) spPush16(v uint16) { c.spPush8(byte(v >> 8)); c.spPush8(byte(v)) }
func (c *CPU) spPull16() uint16 {
	lo := uint16(c.spPull8())
	return lo | uint16(c.spPull8())<<8
}

// The "new" 65816 stack instructions (PEA/PEI/PER/PHD/PLD/JSL/RTL) ignore the
// emulation page-1 wrap while running — SP decrements as a full 16-bit value
// and can leave page 1 — then SPHi is reforced to $01 at the end (emulation).
// In native mode these behave exactly like the ordinary stack helpers.
func (c *CPU) spSetRaw(v uint16) { c.SP, c.SPHi = byte(v), byte(v>>8) }
func (c *CPU) spReforce() {
	if c.E {
		c.SPHi = 0x01
	}
}
func (c *CPU) spPushNew(v byte) {
	c.pinData()
	c.write24(uint32(c.SP16()), v)
	c.spSetRaw(c.SP16() - 1)
}
func (c *CPU) spPullNew() byte {
	c.spSetRaw(c.SP16() + 1)
	c.pinData()
	return c.read24(uint32(c.SP16()))
}
func (c *CPU) spPush16New(v uint16) { c.spPushNew(byte(v >> 8)); c.spPushNew(byte(v)) }
func (c *CPU) spPull16New() uint16 {
	lo := uint16(c.spPullNew())
	return lo | uint16(c.spPullNew())<<8
}

// --- read-modify-write value kernels (width-aware, flag-setting) ---

func (c *CPU) rmwASL(v uint16, wide bool) uint16 {
	if wide {
		c.setFlag(FlagC, v&0x8000 != 0)
		v <<= 1
		c.setZN16(v)
		return v
	}
	b := byte(v)
	c.setFlag(FlagC, b&0x80 != 0)
	b <<= 1
	c.setZN(b)
	return uint16(b)
}

func (c *CPU) rmwLSR(v uint16, wide bool) uint16 {
	if wide {
		c.setFlag(FlagC, v&1 != 0)
		v >>= 1
		c.setZN16(v)
		return v
	}
	b := byte(v)
	c.setFlag(FlagC, b&1 != 0)
	b >>= 1
	c.setZN(b)
	return uint16(b)
}

func (c *CPU) rmwROL(v uint16, wide bool) uint16 {
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	if wide {
		c.setFlag(FlagC, v&0x8000 != 0)
		v = v<<1 | carry
		c.setZN16(v)
		return v
	}
	b := byte(v)
	c.setFlag(FlagC, b&0x80 != 0)
	b = b<<1 | byte(carry)
	c.setZN(b)
	return uint16(b)
}

func (c *CPU) rmwROR(v uint16, wide bool) uint16 {
	if wide {
		carry := uint16(0)
		if c.hasFlag(FlagC) {
			carry = 0x8000
		}
		c.setFlag(FlagC, v&1 != 0)
		v = v>>1 | carry
		c.setZN16(v)
		return v
	}
	carry := byte(0)
	if c.hasFlag(FlagC) {
		carry = 0x80
	}
	b := byte(v)
	c.setFlag(FlagC, b&1 != 0)
	b = b>>1 | carry
	c.setZN(b)
	return uint16(b)
}

func (c *CPU) rmwINC(v uint16, wide bool) uint16 {
	if wide {
		v++
		c.setZN16(v)
		return v
	}
	b := byte(v) + 1
	c.setZN(b)
	return uint16(b)
}

func (c *CPU) rmwDEC(v uint16, wide bool) uint16 {
	if wide {
		v--
		c.setZN16(v)
		return v
	}
	b := byte(v) - 1
	c.setZN(b)
	return uint16(b)
}

func (c *CPU) tsb(v uint16, wide bool) uint16 {
	if wide {
		c.setFlag(FlagZ, c.A16()&v == 0)
		return v | c.A16()
	}
	c.setFlag(FlagZ, uint16(c.A)&v == 0)
	return v | uint16(c.A)
}

func (c *CPU) trb(v uint16, wide bool) uint16 {
	if wide {
		c.setFlag(FlagZ, c.A16()&v == 0)
		return v &^ c.A16()
	}
	c.setFlag(FlagZ, uint16(c.A)&v == 0)
	return v &^ uint16(c.A)
}

// rmwMem reads, transforms, and writes back an accumulator-width memory operand.
// The 16-bit width penalty applies twice (an extra read byte and write byte).
// The 65816's internal modify cycle is a dummy WRITE of the original value back
// to the same address (between the read and the result write), and an indexed
// RMW always pays the extra index cycle (a read of the un-fixed address).
func (c *CPU) rmwMem(e ea816, oc int, fn func(uint16, bool) uint16) int {
	wide := c.mWide()
	c.indexIO(e, true)
	// RMW asserts the memory-lock pin (MLB) for the whole read-modify-write.
	// The real read/write accesses are VDA+MLB; the internal modify cycle is
	// MLB-only (VDA off — the address bus holds but the access is internal).
	data := func() { c.busPins = pinVDA | pinMLB }
	modify := func() { c.busPins = pinMLB }
	if wide {
		hiAddr := c.eaInc(e.addr, e.bank0)
		data()
		lo := uint16(c.read24(e.addr))
		data()
		hi := uint16(c.read24(hiAddr))
		v := fn(lo|hi<<8, true)
		modify()
		c.read24(hiAddr) // internal modify cycle: dummy read of the high byte
		data()
		c.write24(hiAddr, byte(v>>8)) // write the result high byte first, then low
		data()
		c.write24(e.addr, byte(v))
	} else {
		data()
		old := uint16(c.read24(e.addr))
		v := fn(old, false)
		// Internal modify cycle: emulation does a dummy WRITE of the original
		// value; native does a dummy READ.
		modify()
		if c.E {
			c.write24(e.addr, byte(old))
		} else {
			c.read24(e.addr)
		}
		data()
		c.write24(e.addr, byte(v))
	}
	return oc + 3 + 2*b2i(wide) + crossStore(e)
}

// accRMW applies an RMW kernel to the accumulator (2-cycle accumulator form:
// opcode fetch + one internal cycle).
func (c *CPU) accRMW(fn func(uint16, bool) uint16) {
	if c.mWide() {
		c.setA16(fn(c.A16(), true))
	} else {
		c.A = byte(fn(uint16(c.A), false))
	}
	c.io816()
}

// --- control flow ---

func (c *CPU) branch816(take bool) int {
	disp := int8(c.fetch816())
	if !take {
		return 2
	}
	c.ioPC1() // taken branch: internal cycle (dummy read of the operand address)
	target := uint16(int(c.PC) + int(disp))
	cyc := 3
	if c.E && (c.PC&0xFF00) != (target&0xFF00) {
		c.ioPC1() // emulation page-cross: extra internal cycle (also at the operand address)
		cyc = 4
	}
	c.PC = target
	return cyc
}

func (c *CPU) brl() int {
	disp := int16(c.fetch16())
	c.ioPC1() // internal cycle (operand address) before retargeting
	c.PC = uint16(int(c.PC) + int(disp))
	return 4
}

func (c *CPU) plp816() {
	c.P = c.spPull8()
	c.applyWidthTruncation() // re-locks M/X in emulation, narrows index high bytes
}

func (c *CPU) brk() int {
	c.fetch816() // read+skip the signature byte
	cyc := 7
	if c.E {
		c.spPush16(c.PC)
		c.spPush8(c.P | FlagB)
	} else {
		c.spPush8(c.PBR)
		c.spPush16(c.PC)
		c.spPush8(c.P)
		cyc = 8
	}
	c.PBR = 0
	c.setFlag(FlagI, true)
	c.setFlag(FlagD, false)
	vec := uint32(0xFFFE)
	if !c.E {
		vec = 0xFFE6
	}
	c.pinVector()
	c.PC = uint16(c.read24(vec)) | uint16(c.read24(vec+1))<<8
	return cyc
}

// blockMove executes MVN (step +1) / MVP (step -1). The 65816 moves C+1 bytes
// from src bank:X to dst bank:Y using 16-bit X/Y/C regardless of the index/acc
// width, sets DBR to the destination bank, and re-runs the opcode per byte on
// real silicon (so it is interrupt-able mid-block). chippy moves the whole
// block in one Step for debugger sanity, costing 7 cycles per byte. (The Tom
// Harte 65816 corpus caps each block-move test at ~100 cycles mid-instruction,
// a generator artifact this whole-block model deliberately does not replicate,
// so MVN/MVP are exercised by a dedicated unit test rather than the harness.)
func (c *CPU) blockMove(step int) int {
	dst := c.fetch816()
	src := c.fetch816()
	c.DBR = dst
	n := int(c.A16()) + 1
	for {
		c.write24(uint32(dst)<<16|uint32(c.Y16()), c.read24(uint32(src)<<16|uint32(c.X16())))
		c.setX16(uint16(int(c.X16()) + step))
		c.setY16(uint16(int(c.Y16()) + step))
		done := c.A16() == 0
		c.setA16(c.A16() - 1)
		if done {
			break
		}
	}
	return 7 * n
}

func (c *CPU) cop() int {
	c.fetch816() // read+skip the signature byte
	cyc := 7
	if c.E {
		c.spPush16(c.PC)
		c.spPush8(c.P)
	} else {
		c.spPush8(c.PBR)
		c.spPush16(c.PC)
		c.spPush8(c.P)
		cyc = 8
	}
	c.PBR = 0
	c.setFlag(FlagI, true)
	c.setFlag(FlagD, false)
	vec := uint32(0xFFF4)
	if !c.E {
		vec = 0xFFE4
	}
	c.pinVector()
	c.PC = uint16(c.read24(vec)) | uint16(c.read24(vec+1))<<8
	return cyc
}
