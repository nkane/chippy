package cpu

// 65816 opcode kernels (#456 chunk 2): width-aware ALU + memory access wrappers
// shared by the data-movement opcodes. Each wrapper computes the instruction's
// total cycle count from the addressing-mode overhead plus the width and
// indexed-penalty rules derived from the Tom Harte corpus:
//   - +1 if the data is accessed 16-bit (mWide for accumulator ops, xWide for
//     index ops); RMW pays this twice (a read and a write of the extra byte).
//   - indexed reads pay +1 when the index is 16-bit OR the add crossed a page;
//     indexed writes/RMW always pay the extra index cycle.

// indexIO emits the extra indexed-access cycle: a dummy read of the un-fixed
// (non-carry-corrected) address. Reads pay it only on a page cross / 16-bit
// index (crossLoad); writes and RMW always pay it (crossStore).
func (c *CPU) indexIO(e ea816, store bool) {
	if (store && crossStore(e) != 0) || (!store && c.crossLoad(e) != 0) {
		c.read24(e.unfixed)
	}
}

// accRead reads an accumulator-width operand, applies fn, and returns cycles.
func (c *CPU) accRead(e ea816, oc int, fn func(uint16)) int {
	wide := c.mWide()
	c.indexIO(e, false)
	fn(c.readEA(e, wide))
	return oc + 1 + b2i(wide) + c.crossLoad(e)
}

// accWrite stores an accumulator-width value and returns cycles.
func (c *CPU) accWrite(e ea816, oc int, v uint16) int {
	wide := c.mWide()
	c.indexIO(e, true)
	c.writeEA(e, wide, v)
	return oc + 1 + b2i(wide) + crossStore(e)
}

// idxRead / idxWrite are the index-width counterparts (LDX/LDY/STX/STY/CPX/CPY).
func (c *CPU) idxRead(e ea816, oc int, fn func(uint16)) int {
	wide := c.xWide()
	c.indexIO(e, false)
	fn(c.readEA(e, wide))
	return oc + 1 + b2i(wide) + c.crossLoad(e)
}
func (c *CPU) idxWrite(e ea816, oc int, v uint16) int {
	wide := c.xWide()
	c.indexIO(e, true)
	c.writeEA(e, wide, v)
	return oc + 1 + b2i(wide) + crossStore(e)
}

func (c *CPU) oraV(v uint16) {
	if c.mWide() {
		r := c.A16() | v
		c.setA16(r)
		c.setZN16(r)
	} else {
		c.A |= byte(v)
		c.setZN(c.A)
	}
}
func (c *CPU) andV(v uint16) {
	if c.mWide() {
		r := c.A16() & v
		c.setA16(r)
		c.setZN16(r)
	} else {
		c.A &= byte(v)
		c.setZN(c.A)
	}
}
func (c *CPU) eorV(v uint16) {
	if c.mWide() {
		r := c.A16() ^ v
		c.setA16(r)
		c.setZN16(r)
	} else {
		c.A ^= byte(v)
		c.setZN(c.A)
	}
}

func (c *CPU) cmpAccV(v uint16) { c.compare(c.A16(), v, c.mWide()) }

// bitV is the memory BIT: N and V take the operand's two top bits, Z reflects
// the AND with the accumulator. (The immediate form sets only Z — see step816.)
func (c *CPU) bitV(v uint16) {
	if c.mWide() {
		c.setFlag(FlagN, v&0x8000 != 0)
		c.setFlag(FlagV, v&0x4000 != 0)
		c.setFlag(FlagZ, c.A16()&v == 0)
	} else {
		c.setFlag(FlagN, v&0x80 != 0)
		c.setFlag(FlagV, v&0x40 != 0)
		c.setFlag(FlagZ, uint16(c.A)&byte8(v) == 0)
	}
}

func byte8(v uint16) uint16 { return v & 0xFF }

// adc816 / sbc816 — width-aware add/subtract with carry. The 65816 (unlike the
// 65C02) takes no extra cycle in decimal mode, and in decimal mode N/Z/V are
// valid (computed like the CMOS path, extended across all nibbles).
func (c *CPU) adc816(v uint16) {
	carry := uint32(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	wide := c.mWide()
	if c.hasFlag(FlagD) {
		c.adcDecimal816(uint32(v), carry, wide)
		return
	}
	if wide {
		a := uint32(c.A16())
		sum := a + uint32(v) + carry
		c.setFlag(FlagC, sum > 0xFFFF)
		c.setFlag(FlagV, (^(a^uint32(v))&(a^sum))&0x8000 != 0)
		r := uint16(sum)
		c.setA16(r)
		c.setZN16(r)
	} else {
		a := uint16(c.A)
		vv := v & 0xFF
		sum := a + vv + uint16(carry)
		c.setFlag(FlagC, sum > 0xFF)
		c.setFlag(FlagV, (^(a^vv)&(a^sum))&0x80 != 0)
		c.A = byte(sum)
		c.setZN(c.A)
	}
}

func (c *CPU) sbc816(v uint16) {
	carry := uint32(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	wide := c.mWide()
	if c.hasFlag(FlagD) {
		c.sbcDecimal816(uint32(v), carry, wide)
		return
	}
	if wide {
		a := uint32(c.A16())
		w := uint32(v ^ 0xFFFF)
		sum := a + w + carry
		c.setFlag(FlagC, sum > 0xFFFF)
		c.setFlag(FlagV, (^(a^w)&(a^sum))&0x8000 != 0)
		r := uint16(sum)
		c.setA16(r)
		c.setZN16(r)
	} else {
		a := uint16(c.A)
		w := (v ^ 0xFF) & 0xFF
		sum := a + w + uint16(carry)
		c.setFlag(FlagC, sum > 0xFF)
		c.setFlag(FlagV, (^(a^w)&(a^sum))&0x80 != 0)
		c.A = byte(sum)
		c.setZN(c.A)
	}
}

// adcDecimal816 performs packed-BCD add across 2 (8-bit) or 4 (16-bit) nibbles.
// V is latched from the partial sum before the top-nibble decimal correction;
// N/Z reflect the final decimal result (CMOS/65816 semantics).
func (c *CPU) adcDecimal816(v, carry uint32, wide bool) {
	a := uint32(c.A)
	if wide {
		a = uint32(c.A16())
	}
	res := (a & 0x0F) + (v & 0x0F) + carry
	if res >= 0x0A {
		res = ((res + 0x06) & 0x0F) + 0x10
	}
	res = (a & 0xF0) + (v & 0xF0) + res
	if !wide {
		c.setFlag(FlagV, ((a^res)&^(a^v))&0x80 != 0)
		if res >= 0xA0 {
			res += 0x60
		}
		c.setFlag(FlagC, res >= 0x100)
		c.A = byte(res)
		c.setZN(c.A)
		return
	}
	if res >= 0xA0 {
		res = ((res + 0x60) & 0xFF) + 0x100
	}
	res = (a & 0xF00) + (v & 0xF00) + res
	if res >= 0xA00 {
		res = ((res + 0x600) & 0xFFF) + 0x1000
	}
	res = (a & 0xF000) + (v & 0xF000) + res
	c.setFlag(FlagV, ((a^res)&^(a^v))&0x8000 != 0)
	if res >= 0xA000 {
		res += 0x6000
	}
	c.setFlag(FlagC, res >= 0x10000)
	r := uint16(res)
	c.setA16(r)
	c.setZN16(r)
}

// sbcDecimal816 performs packed-BCD subtract; flags C/V come from the binary
// path, N/Z from the decimal result (matching the CMOS implementation).
func (c *CPU) sbcDecimal816(v, carry uint32, wide bool) {
	if wide {
		a := int(c.A16())
		vi := int(v)
		cin := int(carry)
		al := (a & 0x0F) - (vi & 0x0F) + cin - 1
		if al < 0 {
			al = ((al - 0x06) & 0x0F) - 0x10
		}
		al = (a & 0xF0) - (vi & 0xF0) + al
		if al < 0 {
			al = ((al - 0x60) & 0xFF) - 0x100
		}
		al = (a & 0xF00) - (vi & 0xF00) + al
		if al < 0 {
			al = ((al - 0x600) & 0xFFF) - 0x1000
		}
		al = (a & 0xF000) - (vi & 0xF000) + al
		if al < 0 {
			al -= 0x6000
		}
		aw := uint32(c.A16())
		w := uint32(v ^ 0xFFFF)
		sum := aw + w + carry
		c.setFlag(FlagC, sum > 0xFFFF)
		c.setFlag(FlagV, (^(aw^w)&(aw^sum))&0x8000 != 0)
		r := uint16(al)
		c.setA16(r)
		c.setZN16(r)
		return
	}
	a := int(c.A)
	vi := int(v & 0xFF)
	cin := int(carry)
	al := (a & 0x0F) - (vi & 0x0F) + cin - 1
	if al < 0 {
		al = ((al - 0x06) & 0x0F) - 0x10
	}
	res := (a & 0xF0) - (vi & 0xF0) + al
	if res < 0 {
		res -= 0x60
	}
	aw := uint16(c.A)
	w := (uint16(v) ^ 0xFF) & 0xFF
	sum := aw + w + uint16(carry)
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(aw^w)&(aw^sum))&0x80 != 0)
	c.A = byte(res)
	c.setZN(c.A)
}
