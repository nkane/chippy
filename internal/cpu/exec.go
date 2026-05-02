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

func opADC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr)
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	sum := uint16(c.A) + uint16(v) + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^uint16(v))&(uint16(c.A)^sum))&0x80 != 0)
	c.A = byte(sum)
	c.setZN(c.A)
}

func opSBC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr) ^ 0xFF
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	sum := uint16(c.A) + uint16(v) + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^uint16(v))&(uint16(c.A)^sum))&0x80 != 0)
	c.A = byte(sum)
	c.setZN(c.A)
}

func cmp(c *CPU, reg, v byte) {
	c.setFlag(FlagC, reg >= v)
	c.setFlag(FlagZ, reg == v)
	c.setFlag(FlagN, (reg-v)&0x80 != 0)
}
func opCMP(c *CPU, addr uint16, m AddrMode) { cmp(c, c.A, c.Bus.Read(addr)) }
func opCPX(c *CPU, addr uint16, m AddrMode) { cmp(c, c.X, c.Bus.Read(addr)) }
func opCPY(c *CPU, addr uint16, m AddrMode) { cmp(c, c.Y, c.Bus.Read(addr)) }

func opINC(c *CPU, addr uint16, m AddrMode) { v := c.Bus.Read(addr) + 1; c.Bus.Write(addr, v); c.setZN(v) }
func opDEC(c *CPU, addr uint16, m AddrMode) { v := c.Bus.Read(addr) - 1; c.Bus.Write(addr, v); c.setZN(v) }
func opINX(c *CPU, _ uint16, _ AddrMode)    { c.X++; c.setZN(c.X) }
func opINY(c *CPU, _ uint16, _ AddrMode)    { c.Y++; c.setZN(c.Y) }
func opDEX(c *CPU, _ uint16, _ AddrMode)    { c.X--; c.setZN(c.X) }
func opDEY(c *CPU, _ uint16, _ AddrMode)    { c.Y--; c.setZN(c.Y) }

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
