package cpu

// 65816 execution core (#456). step816 is the 65816's own interpreter: it
// fetches at PBR:PC through the 24-bit bus and operates on width-aware
// registers (M/X gated in native mode). This is the from-scratch second core
// the 65816 needs — the 6502 opcode table / 16-bit addressing can't be reused.
//
// CHUNK 1 scope: the register / flag / transfer / immediate instructions that
// touch no data memory (only the PBR:PC instruction stream). Data-memory
// addressing (direct page via D, absolute via DBR, long, [dp], stack-relative,
// MVN/MVP) + ADC/SBC decimal land in later chunks. An unimplemented opcode
// panics with its hex so the harness flags exactly what's left.

// fetch816 reads the next instruction byte at PBR:PC and advances PC (16-bit
// wrap within the bank).
func (c *CPU) fetch816() byte {
	b := c.read24(uint32(c.PBR)<<16 | uint32(c.PC))
	c.PC++
	return b
}

func (c *CPU) setZN16(v uint16) {
	c.setFlag(FlagZ, v == 0)
	c.setFlag(FlagN, v&0x8000 != 0)
}

// readImm reads a 1- or 2-byte immediate operand per the given width and
// returns the value plus the extra cycle a 16-bit operand costs.
func (c *CPU) readImm(wide bool) (uint16, int) {
	lo := uint16(c.fetch816())
	if !wide {
		return lo, 0
	}
	hi := uint16(c.fetch816())
	return lo | hi<<8, 1
}

// step816 executes one 65816 instruction and returns the cycle count.
func (c *CPU) step816() int {
	startPC := c.PC
	op := c.fetch816()
	cyc := 2 // most chunk-1 ops are 2 cycles; adjusted per-op below

	switch op {
	case 0xEA: // NOP
	case 0x18:
		c.setFlag(FlagC, false)
	case 0x38:
		c.setFlag(FlagC, true)
	case 0x58:
		c.setFlag(FlagI, false)
	case 0x78:
		c.setFlag(FlagI, true)
	case 0xD8:
		c.setFlag(FlagD, false)
	case 0xF8:
		c.setFlag(FlagD, true)
	case 0xB8:
		c.setFlag(FlagV, false)

	// --- index inc/dec (X width) ---
	case 0xE8: // INX
		c.incDecIndex(&c.X, &c.XH, +1)
	case 0xCA: // DEX
		c.incDecIndex(&c.X, &c.XH, -1)
	case 0xC8: // INY
		c.incDecIndex(&c.Y, &c.YH, +1)
	case 0x88: // DEY
		c.incDecIndex(&c.Y, &c.YH, -1)

	// --- accumulator inc/dec (M width) ---
	case 0x1A: // INC A
		c.incDecAcc(+1)
	case 0x3A: // DEC A
		c.incDecAcc(-1)

	// --- transfers ---
	case 0xAA: // TAX
		c.transferToIndex(&c.X, &c.XH, c.A, c.B)
	case 0xA8: // TAY
		c.transferToIndex(&c.Y, &c.YH, c.A, c.B)
	case 0xBA: // TSX
		c.transferToIndex(&c.X, &c.XH, c.SP, c.SPHi)
	case 0x8A: // TXA
		c.transferToAcc(c.X, c.XH)
	case 0x98: // TYA
		c.transferToAcc(c.Y, c.YH)
	case 0x9A: // TXS — SP follows index width; emulation keeps SPHi=$01
		c.SP = c.X
		if c.E {
			c.SPHi = 0x01
		} else {
			c.SPHi = c.XH
		}
	case 0x9B: // TXY
		c.transferToIndex(&c.Y, &c.YH, c.X, c.XH)
	case 0xBB: // TYX
		c.transferToIndex(&c.X, &c.XH, c.Y, c.YH)
	case 0x5B: // TCD — C -> D (always 16-bit), sets N/Z on 16-bit
		c.D = c.A16()
		c.setZN16(c.D)
	case 0x7B: // TDC — D -> C (always 16-bit)
		c.setA16(c.D)
		c.setZN16(c.D)
	case 0x1B: // TCS — C -> SP (16-bit; emulation forces high byte $01)
		c.SP = c.A
		if c.E {
			c.SPHi = 0x01
		} else {
			c.SPHi = c.B
		}
	case 0x3B: // TSC — SP -> C (always 16-bit)
		c.setA16(c.SP16())
		c.setZN16(c.SP16())
	case 0xEB: // XBA — swap accumulator bytes; N/Z on the new low byte
		c.A, c.B = c.B, c.A
		c.setZN(c.A)
		cyc = 3

	// --- immediate loads (LD A: M width; LD X/Y: X width) ---
	case 0xA9: // LDA #
		v, e := c.readImm(c.mWide())
		c.loadAcc(v)
		cyc += e
	case 0xA2: // LDX #
		v, e := c.readImm(c.xWide())
		c.loadIndex(&c.X, &c.XH, v)
		cyc += e
	case 0xA0: // LDY #
		v, e := c.readImm(c.xWide())
		c.loadIndex(&c.Y, &c.YH, v)
		cyc += e

	// --- immediate logic (M width) ---
	case 0x29: // AND #
		v, e := c.readImm(c.mWide())
		c.logicAcc(func(a uint16) uint16 { return a & v })
		cyc += e
	case 0x09: // ORA #
		v, e := c.readImm(c.mWide())
		c.logicAcc(func(a uint16) uint16 { return a | v })
		cyc += e
	case 0x49: // EOR #
		v, e := c.readImm(c.mWide())
		c.logicAcc(func(a uint16) uint16 { return a ^ v })
		cyc += e

	// --- immediate compares (CMP: M; CPX/CPY: X) ---
	case 0xC9: // CMP #
		v, e := c.readImm(c.mWide())
		c.compare(c.A16(), v, c.mWide())
		cyc += e
	case 0xE0: // CPX #
		v, e := c.readImm(c.xWide())
		c.compare(c.X16(), v, c.xWide())
		cyc += e
	case 0xC0: // CPY #
		v, e := c.readImm(c.xWide())
		c.compare(c.Y16(), v, c.xWide())
		cyc += e
	case 0x89: // BIT # — only affects Z (immediate form), M width
		v, e := c.readImm(c.mWide())
		c.setFlag(FlagZ, c.A16()&maskFor(c.mWide())&v == 0)
		cyc += e

	// --- mode control ---
	case 0xFB: // XCE
		oldCarry := c.P&FlagC != 0
		c.setFlag(FlagC, c.E)
		c.E = oldCarry
		if c.E {
			c.SPHi = 0x01
			c.XH, c.YH = 0, 0
			c.P |= FlagM | FlagX
		}
	case 0xE2: // SEP #imm
		mask := c.fetch816()
		c.P |= mask
		c.applyWidthTruncation()
		cyc = 3
	case 0xC2: // REP #imm
		mask := c.fetch816()
		c.P &^= mask
		if c.E {
			c.P |= FlagM | FlagX // M/X locked set in emulation
		}
		cyc = 3

	default:
		panic("65816: unimplemented opcode $" + hexByte(op) + " (chunk 1: register/flag/transfer/immediate only)")
	}

	// Self-jump halt heuristic (debugger), matching the 6502 cores.
	if c.PC == startPC {
		c.Halted = true
	}
	c.Cycles += uint64(cyc)
	return cyc
}

// maskFor returns 0xFFFF for a 16-bit width else 0x00FF.
func maskFor(wide bool) uint16 {
	if wide {
		return 0xFFFF
	}
	return 0x00FF
}

func (c *CPU) incDecIndex(lo, hi *byte, delta int) {
	if c.xWide() {
		v := uint16(*hi)<<8 | uint16(*lo)
		v = uint16(int(v) + delta)
		*lo, *hi = byte(v), byte(v>>8)
		c.setZN16(v)
	} else {
		*lo = byte(int(*lo) + delta)
		c.setZN(*lo)
	}
}

func (c *CPU) incDecAcc(delta int) {
	if c.mWide() {
		v := uint16(int(c.A16()) + delta)
		c.setA16(v)
		c.setZN16(v)
	} else {
		c.A = byte(int(c.A) + delta)
		c.setZN(c.A)
	}
}

// transferToIndex copies a source register into an index register at the index
// width, setting N/Z on the result.
func (c *CPU) transferToIndex(dlo, dhi *byte, slo, shi byte) {
	if c.xWide() {
		v := uint16(shi)<<8 | uint16(slo)
		*dlo, *dhi = byte(v), byte(v>>8)
		c.setZN16(v)
	} else {
		*dlo = slo
		c.setZN(slo)
	}
}

// transferToAcc copies a source register into the accumulator at the
// accumulator width.
func (c *CPU) transferToAcc(slo, shi byte) {
	if c.mWide() {
		v := uint16(shi)<<8 | uint16(slo)
		c.setA16(v)
		c.setZN16(v)
	} else {
		c.A = slo
		c.setZN(c.A)
	}
}

func (c *CPU) loadAcc(v uint16) {
	if c.mWide() {
		c.setA16(v)
		c.setZN16(v)
	} else {
		c.A = byte(v)
		c.setZN(c.A)
	}
}

func (c *CPU) loadIndex(lo, hi *byte, v uint16) {
	if c.xWide() {
		*lo, *hi = byte(v), byte(v>>8)
		c.setZN16(v)
	} else {
		*lo = byte(v)
		c.setZN(*lo)
	}
}

func (c *CPU) logicAcc(fn func(uint16) uint16) {
	if c.mWide() {
		v := fn(c.A16())
		c.setA16(v)
		c.setZN16(v)
	} else {
		v := byte(fn(uint16(c.A)))
		c.A = v
		c.setZN(v)
	}
}

// compare sets C/Z/N from reg-val at the given width without storing.
func (c *CPU) compare(reg, val uint16, wide bool) {
	if wide {
		d := reg - val
		c.setFlag(FlagC, reg >= val)
		c.setFlag(FlagZ, d == 0)
		c.setFlag(FlagN, d&0x8000 != 0)
	} else {
		r, v := byte(reg), byte(val)
		d := r - v
		c.setFlag(FlagC, r >= v)
		c.setFlag(FlagZ, d == 0)
		c.setFlag(FlagN, d&0x80 != 0)
	}
}

// applyWidthTruncation enforces register widths after a SEP that narrows M/X:
// narrowing the index width clears the index high bytes.
func (c *CPU) applyWidthTruncation() {
	if c.E {
		c.P |= FlagM | FlagX
	}
	if !c.xWide() {
		c.XH, c.YH = 0, 0
	}
}

func hexByte(b byte) string {
	const d = "0123456789ABCDEF"
	return string([]byte{d[b>>4], d[b&0xF]})
}
