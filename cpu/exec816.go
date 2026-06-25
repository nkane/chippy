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

	// === chunk 2: data-movement / ALU memory ops ===

	// --- ORA ---
	case 0x01:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x03:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x05:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x07:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x0D:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x0F:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x11:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x12:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x13:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x15:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.oraV)
	case 0x17:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.oraV)
	case 0x19:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.oraV)
	case 0x1D:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.oraV)
	case 0x1F:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.oraV)

	// --- AND ---
	case 0x21:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.andV)
	case 0x23:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.andV)
	case 0x25:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.andV)
	case 0x27:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.andV)
	case 0x2D:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.andV)
	case 0x2F:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.andV)
	case 0x31:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.andV)
	case 0x32:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.andV)
	case 0x33:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.andV)
	case 0x35:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.andV)
	case 0x37:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.andV)
	case 0x39:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.andV)
	case 0x3D:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.andV)
	case 0x3F:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.andV)

	// --- EOR ---
	case 0x41:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x43:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x45:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x47:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x4D:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x4F:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x51:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x52:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x53:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x55:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.eorV)
	case 0x57:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.eorV)
	case 0x59:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.eorV)
	case 0x5D:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.eorV)
	case 0x5F:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.eorV)

	// --- ADC ---
	case 0x61:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x63:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x65:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x67:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x69: // ADC #
		v, e := c.readImm(c.mWide())
		c.adc816(v)
		cyc += e
	case 0x6D:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x6F:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x71:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x72:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x73:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x75:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.adc816)
	case 0x77:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.adc816)
	case 0x79:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.adc816)
	case 0x7D:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.adc816)
	case 0x7F:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.adc816)

	// --- STA ---
	case 0x81:
		e, oc := c.amIndDPX()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x83:
		e, oc := c.amStackRel()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x85:
		e, oc := c.amDP()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x87:
		e, oc := c.amIndLongDP()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x8D:
		e, oc := c.amAbs()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x8F:
		e, oc := c.amLong()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x91:
		e, oc := c.amIndDPY()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x92:
		e, oc := c.amIndDP()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x93:
		e, oc := c.amStackRelIndY()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x95:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accWrite(e, oc, c.A16())
	case 0x97:
		e, oc := c.amIndLongDPY()
		cyc = c.accWrite(e, oc, c.A16())
	case 0x99:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accWrite(e, oc, c.A16())
	case 0x9D:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accWrite(e, oc, c.A16())
	case 0x9F:
		e, oc := c.amLongX()
		cyc = c.accWrite(e, oc, c.A16())

	// --- LDA ---
	case 0xA1:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xA3:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xA5:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xA7:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xAD:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xAF:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xB1:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xB2:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xB3:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xB5:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xB7:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xB9:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xBD:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.loadAcc)
	case 0xBF:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.loadAcc)

	// --- CMP ---
	case 0xC1:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xC3:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xC5:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xC7:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xCD:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xCF:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xD1:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xD2:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xD3:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xD5:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xD7:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xD9:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xDD:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.cmpAccV)
	case 0xDF:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.cmpAccV)

	// --- SBC ---
	case 0xE1:
		e, oc := c.amIndDPX()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xE3:
		e, oc := c.amStackRel()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xE5:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xE7:
		e, oc := c.amIndLongDP()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xE9: // SBC #
		v, e := c.readImm(c.mWide())
		c.sbc816(v)
		cyc += e
	case 0xED:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xEF:
		e, oc := c.amLong()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xF1:
		e, oc := c.amIndDPY()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xF2:
		e, oc := c.amIndDP()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xF3:
		e, oc := c.amStackRelIndY()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xF5:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xF7:
		e, oc := c.amIndLongDPY()
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xF9:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xFD:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.sbc816)
	case 0xFF:
		e, oc := c.amLongX()
		cyc = c.accRead(e, oc, c.sbc816)

	// --- LDX / LDY ---
	case 0xA6:
		e, oc := c.amDP()
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.X, &c.XH, v) })
	case 0xAE:
		e, oc := c.amAbs()
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.X, &c.XH, v) })
	case 0xB6:
		e, oc := c.amDPIdx(c.Yidx())
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.X, &c.XH, v) })
	case 0xBE:
		e, oc := c.amAbsIdx(c.Yidx())
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.X, &c.XH, v) })
	case 0xA4:
		e, oc := c.amDP()
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.Y, &c.YH, v) })
	case 0xAC:
		e, oc := c.amAbs()
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.Y, &c.YH, v) })
	case 0xB4:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.Y, &c.YH, v) })
	case 0xBC:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.idxRead(e, oc, func(v uint16) { c.loadIndex(&c.Y, &c.YH, v) })

	// --- STX / STY ---
	case 0x86:
		e, oc := c.amDP()
		cyc = c.idxWrite(e, oc, c.X16())
	case 0x8E:
		e, oc := c.amAbs()
		cyc = c.idxWrite(e, oc, c.X16())
	case 0x96:
		e, oc := c.amDPIdx(c.Yidx())
		cyc = c.idxWrite(e, oc, c.X16())
	case 0x84:
		e, oc := c.amDP()
		cyc = c.idxWrite(e, oc, c.Y16())
	case 0x8C:
		e, oc := c.amAbs()
		cyc = c.idxWrite(e, oc, c.Y16())
	case 0x94:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.idxWrite(e, oc, c.Y16())

	// --- STZ ---
	case 0x64:
		e, oc := c.amDP()
		cyc = c.accWrite(e, oc, 0)
	case 0x74:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accWrite(e, oc, 0)
	case 0x9C:
		e, oc := c.amAbs()
		cyc = c.accWrite(e, oc, 0)
	case 0x9E:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accWrite(e, oc, 0)

	// --- CPX / CPY ---
	case 0xE4:
		e, oc := c.amDP()
		cyc = c.idxRead(e, oc, func(v uint16) { c.compare(c.X16(), v, c.xWide()) })
	case 0xEC:
		e, oc := c.amAbs()
		cyc = c.idxRead(e, oc, func(v uint16) { c.compare(c.X16(), v, c.xWide()) })
	case 0xC4:
		e, oc := c.amDP()
		cyc = c.idxRead(e, oc, func(v uint16) { c.compare(c.Y16(), v, c.xWide()) })
	case 0xCC:
		e, oc := c.amAbs()
		cyc = c.idxRead(e, oc, func(v uint16) { c.compare(c.Y16(), v, c.xWide()) })

	// --- BIT (memory forms; immediate $89 is handled above, Z-only) ---
	case 0x24:
		e, oc := c.amDP()
		cyc = c.accRead(e, oc, c.bitV)
	case 0x2C:
		e, oc := c.amAbs()
		cyc = c.accRead(e, oc, c.bitV)
	case 0x34:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.bitV)
	case 0x3C:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.accRead(e, oc, c.bitV)

	// === chunk 3: RMW / stack / control flow ===

	// --- shifts/rotates (accumulator + memory) ---
	case 0x0A:
		c.accRMW(c.rmwASL)
	case 0x06:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.rmwASL)
	case 0x0E:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.rmwASL)
	case 0x16:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwASL)
	case 0x1E:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwASL)
	case 0x4A:
		c.accRMW(c.rmwLSR)
	case 0x46:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.rmwLSR)
	case 0x4E:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.rmwLSR)
	case 0x56:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwLSR)
	case 0x5E:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwLSR)
	case 0x2A:
		c.accRMW(c.rmwROL)
	case 0x26:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.rmwROL)
	case 0x2E:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.rmwROL)
	case 0x36:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwROL)
	case 0x3E:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwROL)
	case 0x6A:
		c.accRMW(c.rmwROR)
	case 0x66:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.rmwROR)
	case 0x6E:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.rmwROR)
	case 0x76:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwROR)
	case 0x7E:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwROR)

	// --- INC/DEC memory ---
	case 0xE6:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.rmwINC)
	case 0xEE:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.rmwINC)
	case 0xF6:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwINC)
	case 0xFE:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwINC)
	case 0xC6:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.rmwDEC)
	case 0xCE:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.rmwDEC)
	case 0xD6:
		e, oc := c.amDPIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwDEC)
	case 0xDE:
		e, oc := c.amAbsIdx(c.Xidx())
		cyc = c.rmwMem(e, oc, c.rmwDEC)

	// --- TSB/TRB ---
	case 0x04:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.tsb)
	case 0x0C:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.tsb)
	case 0x14:
		e, oc := c.amDP()
		cyc = c.rmwMem(e, oc, c.trb)
	case 0x1C:
		e, oc := c.amAbs()
		cyc = c.rmwMem(e, oc, c.trb)

	// --- stack push/pull ---
	case 0x48: // PHA
		if c.mWide() {
			c.spPush16(c.A16())
		} else {
			c.spPush8(c.A)
		}
		cyc = 3 + b2i(c.mWide())
	case 0x68: // PLA
		if c.mWide() {
			v := c.spPull16()
			c.setA16(v)
			c.setZN16(v)
		} else {
			c.A = c.spPull8()
			c.setZN(c.A)
		}
		cyc = 4 + b2i(c.mWide())
	case 0xDA: // PHX
		if c.xWide() {
			c.spPush16(c.X16())
		} else {
			c.spPush8(c.X)
		}
		cyc = 3 + b2i(c.xWide())
	case 0xFA: // PLX
		if c.xWide() {
			v := c.spPull16()
			c.setX16(v)
			c.setZN16(v)
		} else {
			c.X = c.spPull8()
			c.setZN(c.X)
		}
		cyc = 4 + b2i(c.xWide())
	case 0x5A: // PHY
		if c.xWide() {
			c.spPush16(c.Y16())
		} else {
			c.spPush8(c.Y)
		}
		cyc = 3 + b2i(c.xWide())
	case 0x7A: // PLY
		if c.xWide() {
			v := c.spPull16()
			c.setY16(v)
			c.setZN16(v)
		} else {
			c.Y = c.spPull8()
			c.setZN(c.Y)
		}
		cyc = 4 + b2i(c.xWide())
	case 0x08: // PHP
		c.spPush8(c.P)
		cyc = 3
	case 0x28: // PLP
		c.plp816()
		cyc = 4
	case 0x8B: // PHB
		c.spPush8(c.DBR)
		cyc = 3
	case 0xAB: // PLB
		c.DBR = c.spPull8()
		c.setZN(c.DBR)
		cyc = 4
	case 0x4B: // PHK
		c.spPush8(c.PBR)
		cyc = 3
	case 0x0B: // PHD
		c.spPush16New(c.D)
		c.spReforce()
		cyc = 4
	case 0x2B: // PLD
		c.D = c.spPull16New()
		c.spReforce()
		c.setZN16(c.D)
		cyc = 5
	case 0xF4: // PEA
		c.spPush16New(c.fetch16())
		c.spReforce()
		cyc = 5
	case 0xD4: // PEI
		dp := c.fetch816()
		c.spPush16New(c.readDPWordWrap(dp))
		c.spReforce()
		cyc = 6 + c.dlPenalty()
	case 0x62: // PER
		disp := int16(c.fetch16())
		c.spPush16New(uint16(int(c.PC) + int(disp)))
		c.spReforce()
		cyc = 6

	// --- jumps / calls / returns ---
	case 0x4C: // JMP abs
		c.PC = c.fetch16()
		cyc = 3
	case 0x5C: // JML long
		t := c.fetch24()
		c.PBR = byte(t >> 16)
		c.PC = uint16(t)
		cyc = 4
	case 0x6C: // JMP (abs)
		ptr := c.fetch16()
		c.PC = uint16(c.read24(uint32(ptr))) | uint16(c.read24(uint32((ptr+1)&0xFFFF)))<<8
		cyc = 5
	case 0x7C: // JMP (abs,X)
		a := uint32(c.PBR)<<16 | uint32(c.fetch16()+c.Xidx())
		c.PC = uint16(c.read24(a)) | uint16(c.read24(bankInc(a)))<<8
		cyc = 6
	case 0xDC: // JML [abs]
		ptr := c.fetch16()
		lo := uint16(c.read24(uint32(ptr)))
		hi := uint16(c.read24(uint32((ptr + 1) & 0xFFFF)))
		c.PBR = c.read24(uint32((ptr + 2) & 0xFFFF))
		c.PC = lo | hi<<8
		cyc = 6
	case 0x20: // JSR abs
		t := c.fetch16()
		c.spPush16(c.PC - 1)
		c.PC = t
		cyc = 6
	case 0x22: // JSL long
		t := c.fetch24()
		c.spPushNew(c.PBR)
		c.spPush16New(c.PC - 1)
		c.spReforce()
		c.PBR = byte(t >> 16)
		c.PC = uint16(t)
		cyc = 8
	case 0xFC: // JSR (abs,X)
		a := uint32(c.PBR)<<16 | uint32(c.fetch16()+c.Xidx())
		c.spPush16(c.PC - 1)
		c.PC = uint16(c.read24(a)) | uint16(c.read24(bankInc(a)))<<8
		cyc = 8
	case 0x60: // RTS
		c.PC = c.spPull16() + 1
		cyc = 6
	case 0x6B: // RTL
		lo := c.spPull16New()
		c.PBR = c.spPullNew()
		c.spReforce()
		c.PC = lo + 1
		cyc = 6
	case 0x40: // RTI
		c.plp816()
		c.PC = c.spPull16()
		cyc = 6
		if !c.E {
			c.PBR = c.spPull8()
			cyc = 7
		}

	// --- branches ---
	case 0x10:
		cyc = c.branch816(!c.hasFlag(FlagN))
	case 0x30:
		cyc = c.branch816(c.hasFlag(FlagN))
	case 0x50:
		cyc = c.branch816(!c.hasFlag(FlagV))
	case 0x70:
		cyc = c.branch816(c.hasFlag(FlagV))
	case 0x90:
		cyc = c.branch816(!c.hasFlag(FlagC))
	case 0xB0:
		cyc = c.branch816(c.hasFlag(FlagC))
	case 0xD0:
		cyc = c.branch816(!c.hasFlag(FlagZ))
	case 0xF0:
		cyc = c.branch816(c.hasFlag(FlagZ))
	case 0x80: // BRA
		cyc = c.branch816(true)
	case 0x82: // BRL
		cyc = c.brl()

	// --- interrupts / misc ---
	case 0x00: // BRK
		cyc = c.brk()
	case 0x02: // COP
		cyc = c.cop()
	case 0x42: // WDM (2-byte no-op)
		c.fetch816()
	case 0xCB: // WAI
		c.Halted = true
		cyc = 4
	case 0xDB: // STP
		c.Halted = true
		c.stoppedBySTP = true
		cyc = 4

	case 0x54: // MVN — block move, ascending
		cyc = c.blockMove(+1)
	case 0x44: // MVP — block move, descending
		cyc = c.blockMove(-1)

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
