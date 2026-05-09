package cpu

// Step executes one instruction. Returns cycles consumed.
// If the instruction is an infinite tight loop (PC unchanged afterwards) the
// CPU is marked Halted so callers can stop free-running.
func (c *CPU) Step() int {
	if c.Halted {
		return 0
	}
	startPC := c.PC
	op := c.Bus.Read(c.PC)
	c.PC++
	in := Opcodes[op]
	addr, pageCrossed := c.resolve(in.Mode)
	in.Exec(c, addr, in.Mode)
	cycles := in.Cycles
	if in.PageAdd && pageCrossed {
		cycles++
	}
	c.Cycles += uint64(cycles)
	// Self-jump detection: a `JMP self` (or any instruction that leaves PC
	// pointing back at its own opcode) means the program has halted.
	if c.PC == startPC {
		c.Halted = true
	}
	return cycles
}

// --- helpers for read/write w/ ACC mode ---
func (c *CPU) load(addr uint16, mode AddrMode) byte {
	if mode == ACC {
		return c.A
	}
	return c.Bus.Read(addr)
}
func (c *CPU) store(addr uint16, mode AddrMode, v byte) {
	if mode == ACC {
		c.A = v
		return
	}
	c.Bus.Write(addr, v)
}

// --- ops ---

func opLDA(c *CPU, addr uint16, m AddrMode) { c.A = c.Bus.Read(addr); c.setZN(c.A) }
func opLDX(c *CPU, addr uint16, m AddrMode) { c.X = c.Bus.Read(addr); c.setZN(c.X) }
func opLDY(c *CPU, addr uint16, m AddrMode) { c.Y = c.Bus.Read(addr); c.setZN(c.Y) }

func opSTA(c *CPU, addr uint16, m AddrMode) { c.Bus.Write(addr, c.A) }
func opSTX(c *CPU, addr uint16, m AddrMode) { c.Bus.Write(addr, c.X) }
func opSTY(c *CPU, addr uint16, m AddrMode) { c.Bus.Write(addr, c.Y) }

func opTAX(c *CPU, _ uint16, _ AddrMode) { c.X = c.A; c.setZN(c.X) }
func opTAY(c *CPU, _ uint16, _ AddrMode) { c.Y = c.A; c.setZN(c.Y) }
func opTSX(c *CPU, _ uint16, _ AddrMode) { c.X = c.SP; c.setZN(c.X) }
func opTXA(c *CPU, _ uint16, _ AddrMode) { c.A = c.X; c.setZN(c.A) }
func opTXS(c *CPU, _ uint16, _ AddrMode) { c.SP = c.X }
func opTYA(c *CPU, _ uint16, _ AddrMode) { c.A = c.Y; c.setZN(c.A) }

func opPHA(c *CPU, _ uint16, _ AddrMode) { c.push(c.A) }
func opPHP(c *CPU, _ uint16, _ AddrMode) { c.push(c.P | FlagB | FlagU) }
func opPLA(c *CPU, _ uint16, _ AddrMode) { c.A = c.pop(); c.setZN(c.A) }
func opPLP(c *CPU, _ uint16, _ AddrMode) { c.P = (c.pop() &^ FlagB) | FlagU }

func opAND(c *CPU, addr uint16, m AddrMode) { c.A &= c.Bus.Read(addr); c.setZN(c.A) }
func opEOR(c *CPU, addr uint16, m AddrMode) { c.A ^= c.Bus.Read(addr); c.setZN(c.A) }
func opORA(c *CPU, addr uint16, m AddrMode) { c.A |= c.Bus.Read(addr); c.setZN(c.A) }

func opBIT(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr)
	c.setFlag(FlagZ, c.A&v == 0)
	c.setFlag(FlagN, v&0x80 != 0)
	c.setFlag(FlagV, v&0x40 != 0)
}

// opADC — Add with Carry. Branches on the D flag for packed-BCD mode.
// NMOS 6502 BCD semantics per Bruce Clark
// (http://www.6502.org/tutorials/decimal_mode.html):
//   - C is set correctly for the decimal result
//   - Z reflects the *binary* result (so it's effectively undefined for BCD
//     operands, matching real silicon behaviour)
//   - N and V reflect the partially-adjusted high nibble (also "undefined"
//     on NMOS but deterministic, and Bruce Clark's vectors check them)
func opADC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr)
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	if c.hasFlag(FlagD) {
		adcDecimal(c, v, carry)
		return
	}
	sum := uint16(c.A) + uint16(v) + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^uint16(v))&(uint16(c.A)^sum))&0x80 != 0)
	c.A = byte(sum)
	c.setZN(c.A)
}

// opSBC — Subtract with Carry (carry acts as ~borrow). Branches on D for BCD.
// NMOS BCD: N/V/Z/C are computed from the *binary* path (matching real
// hardware where those flags are documented as undefined but in fact reflect
// the parallel binary subtract); A holds the decimal result.
func opSBC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr)
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	if c.hasFlag(FlagD) {
		sbcDecimal(c, v, carry)
		return
	}
	w := v ^ 0xFF
	sum := uint16(c.A) + uint16(w) + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^uint16(w))&(uint16(c.A)^sum))&0x80 != 0)
	c.A = byte(sum)
	c.setZN(c.A)
}

// adcDecimal performs the NMOS packed-BCD ADC. carry is 0 or 1.
func adcDecimal(c *CPU, v byte, carry uint16) {
	a := uint16(c.A)
	// Low nibble add with carry-in.
	al := (a & 0x0F) + (uint16(v) & 0x0F) + carry
	if al >= 0x0A {
		al = ((al + 0x06) & 0x0F) + 0x10
	}
	// High nibble add (with the possibly-bumped low result already shifted).
	ah := (a & 0xF0) + (uint16(v) & 0xF0) + al // signed-ish wide add
	// N and V latched from the partially-adjusted AH.
	c.setFlag(FlagN, ah&0x80 != 0)
	c.setFlag(FlagV, ((a^ah)&^(a^uint16(v)))&0x80 != 0)
	// Z reflects the binary sum (NMOS quirk).
	bin := byte(a + uint16(v) + carry)
	c.setFlag(FlagZ, bin == 0)
	if ah >= 0xA0 {
		ah += 0x60
	}
	c.setFlag(FlagC, ah >= 0x100)
	c.A = byte(ah & 0xFF)
}

// sbcDecimal performs the NMOS packed-BCD SBC. carry is 0 or 1 (1 = no borrow).
func sbcDecimal(c *CPU, v byte, carry uint16) {
	a := int(c.A)
	vi := int(v)
	cin := int(carry)
	// Decimal path — A result.
	al := (a & 0x0F) - (vi & 0x0F) + cin - 1
	if al < 0 {
		al = ((al - 0x06) & 0x0F) - 0x10
	}
	res := (a & 0xF0) - (vi & 0xF0) + al
	if res < 0 {
		res -= 0x60
	}
	// Binary path drives all flags.
	w := v ^ 0xFF
	sum := uint16(c.A) + uint16(w) + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^uint16(w))&(uint16(c.A)^sum))&0x80 != 0)
	c.setFlag(FlagN, sum&0x80 != 0)
	c.setFlag(FlagZ, byte(sum) == 0)
	c.A = byte(res & 0xFF)
}

func cmp(c *CPU, reg, v byte) {
	c.setFlag(FlagC, reg >= v)
	c.setFlag(FlagZ, reg == v)
	c.setFlag(FlagN, (reg-v)&0x80 != 0)
}
func opCMP(c *CPU, addr uint16, m AddrMode) { cmp(c, c.A, c.Bus.Read(addr)) }
func opCPX(c *CPU, addr uint16, m AddrMode) { cmp(c, c.X, c.Bus.Read(addr)) }
func opCPY(c *CPU, addr uint16, m AddrMode) { cmp(c, c.Y, c.Bus.Read(addr)) }

func opINC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr) + 1
	c.Bus.Write(addr, v)
	c.setZN(v)
}
func opDEC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr) - 1
	c.Bus.Write(addr, v)
	c.setZN(v)
}
func opINX(c *CPU, _ uint16, _ AddrMode) { c.X++; c.setZN(c.X) }
func opINY(c *CPU, _ uint16, _ AddrMode) { c.Y++; c.setZN(c.Y) }
func opDEX(c *CPU, _ uint16, _ AddrMode) { c.X--; c.setZN(c.X) }
func opDEY(c *CPU, _ uint16, _ AddrMode) { c.Y--; c.setZN(c.Y) }

func opASL(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	c.setFlag(FlagC, v&0x80 != 0)
	v <<= 1
	c.store(addr, m, v)
	c.setZN(v)
}
func opLSR(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	c.setFlag(FlagC, v&1 != 0)
	v >>= 1
	c.store(addr, m, v)
	c.setZN(v)
}
func opROL(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	carryIn := byte(0)
	if c.hasFlag(FlagC) {
		carryIn = 1
	}
	c.setFlag(FlagC, v&0x80 != 0)
	v = (v << 1) | carryIn
	c.store(addr, m, v)
	c.setZN(v)
}
func opROR(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	carryIn := byte(0)
	if c.hasFlag(FlagC) {
		carryIn = 0x80
	}
	c.setFlag(FlagC, v&1 != 0)
	v = (v >> 1) | carryIn
	c.store(addr, m, v)
	c.setZN(v)
}

func opJMP(c *CPU, addr uint16, _ AddrMode) { c.PC = addr }
func opJSR(c *CPU, addr uint16, _ AddrMode) {
	c.push16(c.PC - 1)
	c.PC = addr
}
func opRTS(c *CPU, _ uint16, _ AddrMode) { c.PC = c.pop16() + 1 }
func opRTI(c *CPU, _ uint16, _ AddrMode) {
	c.P = (c.pop() &^ FlagB) | FlagU
	c.PC = c.pop16()
}

func branch(c *CPU, addr uint16, take bool) {
	if !take {
		return
	}
	// extra cycle, +1 if page crossed
	c.Cycles++
	if (c.PC & 0xFF00) != (addr & 0xFF00) {
		c.Cycles++
	}
	c.PC = addr
}
func opBPL(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, !c.hasFlag(FlagN)) }
func opBMI(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, c.hasFlag(FlagN)) }
func opBVC(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, !c.hasFlag(FlagV)) }
func opBVS(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, c.hasFlag(FlagV)) }
func opBCC(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, !c.hasFlag(FlagC)) }
func opBCS(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, c.hasFlag(FlagC)) }
func opBNE(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, !c.hasFlag(FlagZ)) }
func opBEQ(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, c.hasFlag(FlagZ)) }

func opCLC(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagC, false) }
func opSEC(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagC, true) }
func opCLI(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagI, false) }
func opSEI(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagI, true) }
func opCLV(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagV, false) }
func opCLD(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagD, false) }
func opSED(c *CPU, _ uint16, _ AddrMode) { c.setFlag(FlagD, true) }

func opBRK(c *CPU, _ uint16, _ AddrMode) {
	c.PC++
	c.push16(c.PC)
	c.push(c.P | FlagB | FlagU)
	c.setFlag(FlagI, true)
	lo := uint16(c.Bus.Read(VecIRQ))
	hi := uint16(c.Bus.Read(VecIRQ + 1))
	c.PC = lo | hi<<8
}
func opNOP(c *CPU, _ uint16, _ AddrMode) {}
