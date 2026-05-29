package cpu

import "fmt"

// Step executes one instruction. Returns cycles consumed.
// If the instruction is an infinite tight loop (PC unchanged afterwards) the
// CPU is marked Halted so callers can stop free-running.
//
// Pending interrupts are serviced at the instruction boundary BEFORE the
// next opcode is fetched. NMI is checked first (always taken) then IRQ
// (only if FlagI is clear). Servicing an interrupt clears Halted so a
// program spinning in a wait-loop can be woken by a peripheral.
//
// CMOS halt variants:
//   - WAI ($CB) halts the CPU until any IRQ or NMI is signalled. The wake
//     happens even when FlagI is set: if the line is asserted but masked,
//     execution resumes at the instruction *after* WAI (no service).
//   - STP ($DB) halts until external reset. Interrupts are ignored while
//     STP-halted; the only way out is c.Reset(). stoppedBySTP gates the
//     interrupt-service block below.
func (c *CPU) Step() int {
	// STP-halt: ignore everything until Reset clears stoppedBySTP.
	if c.stoppedBySTP {
		return 0
	}
	// WAI-wake on masked IRQ: line is asserted, FlagI prevents servicing,
	// but WAI still un-halts so execution continues with the next opcode.
	if c.Halted && c.irqLine && c.hasFlag(FlagI) && !c.nmiPending {
		c.Halted = false
	}
	// Service pending interrupts at the boundary. An interrupt service is
	// itself a 7-cycle operation; we do NOT also execute an instruction in
	// the same Step. The next call to Step will fetch the first opcode of
	// the handler.
	//
	// NMI recognition differs by variant. NES (#342) uses nmiDue, the
	// poll latch set before the previous instruction's final cycle, so an
	// NMI asserted on that last cycle (e.g. a $2000 write that enables NMI
	// while the vblank flag is already set) is delayed one instruction —
	// matching the 6502's interrupt-poll timing. NMOS/CMOS keep the
	// immediate edge-service path.
	nmiTake := c.nmiPending
	if c.Variant == VariantNES {
		nmiTake = c.nmiDue
	}
	if nmiTake {
		c.Halted = false
		if c.Tracer != nil {
			c.Tracer.LogInterrupt(c, "NMI", VecNMI)
		}
		before := c.Cycles
		c.nmiDue = false
		c.serviceNMI()
		return int(c.Cycles - before)
	}
	irqTake := c.irqLine && !c.hasFlag(FlagI)
	if c.Variant == VariantNES {
		// Penultimate-cycle poll: CLI/SEI/PLP delay the mask change one
		// instruction. cpu_interrupts_v2 cli_latency (#369).
		irqTake = c.irqDue
	}
	if irqTake {
		c.Halted = false
		if c.Tracer != nil {
			c.Tracer.LogInterrupt(c, "IRQ", VecIRQ)
		}
		before := c.Cycles
		c.irqDue = false
		c.serviceIRQ()
		return int(c.Cycles - before)
	}

	if c.Halted {
		return 0
	}
	if c.Tracer != nil {
		c.Tracer.LogStep(c, c.Bus)
	}
	startPC := c.PC
	// Per-cycle interleave (#342, VariantNES): every bus access ticks the
	// chain one cycle, so the PPU/APU/cart run in 1:1 lockstep with the
	// CPU and a $2002 read samples the PPU at its true dot. instrCycles
	// counts those ticks; it must equal the instruction's accounted total
	// (asserted below). Other variants keep the post-instruction batch
	// tick — their byte-for-byte behavior is unchanged.
	c.instrCycles = 0
	op := c.read(c.PC) // cycle 1: opcode fetch
	c.PC++
	in := c.opcodes[op]
	addr, pageCrossed := c.resolve(in.Mode)
	c.extraCycles = 0
	if c.nesCycle {
		c.addrDummies(in, addr, pageCrossed)
	} else if c.Variant == VariantNES {
		// NES without a bus ticker can't interleave, but still polls so
		// the nmiDue latch advances and NMIs get serviced.
		c.nmiDue = c.nmiPending
	}
	in.Exec(c, addr, in.Mode)
	cycles := in.Cycles + c.extraCycles
	if in.PageAdd && pageCrossed {
		cycles++
	}
	c.Cycles += uint64(cycles)
	if c.nesCycle {
		if c.instrCycles != cycles {
			panic(fmt.Sprintf("cpu: cycle mismatch %s mode=%d ticked=%d want=%d",
				in.Name, in.Mode, c.instrCycles, cycles))
		}
	} else if c.busTicker != nil {
		c.busTicker.Tick(cycles)
	}
	// Self-jump detection: a `JMP self` (or any instruction that leaves PC
	// pointing back at its own opcode) means the program has halted —
	// but ONLY on the chippy debugger variants (NMOS, 65C02), where the
	// heuristic powers the TUI's "halted (press R)" status line and is
	// documented behavior. NES programs legitimately spin on `JMP self`
	// while waiting for NMI: the main loop sits in a tight spin and the
	// NMI handler does all the per-frame work. Halt-marking that idle
	// would stall the bus-ticker fan-out (Step returns 0 once Halted)
	// and freeze the PPU. Skip the heuristic on VariantNES so the
	// canonical NES idle pattern keeps the system clock running.
	if c.PC == startPC && c.Variant != VariantNES {
		c.Halted = true
	}
	return cycles
}

// addrDummies issues the dummy bus cycles an addressing mode performs
// beyond its explicit operand + data accesses, so the per-cycle tick
// count matches the instruction's true length (#342). Zero-page-indexed
// dummies live in resolve; multi-cycle stack / control / RMW dummies
// live in their handlers. This covers the implied/accumulator internal
// cycle and the absolute/indirect-indexed fix-up read.
func (c *CPU) addrDummies(in Instr, addr uint16, pageCrossed bool) {
	switch in.Mode {
	case IMP, ACC:
		// 2-cycle implied/accumulator ops do a dummy read of the next
		// byte. Multi-cycle implied ops (stack, RTS/RTI/BRK) carry their
		// own dummies in the handler, so gate on the 2-cycle length.
		if in.Cycles == 2 {
			c.idle(c.PC)
		}
	case ABX:
		// Indexed read: extra fix-up read only when the page is crossed
		// (PageAdd). Indexed write / RMW (PageAdd false): always.
		if !in.PageAdd || pageCrossed {
			base := addr - uint16(c.X)
			c.idle((base & 0xFF00) | (addr & 0x00FF))
		}
	case ABY, IZY:
		if !in.PageAdd || pageCrossed {
			base := addr - uint16(c.Y)
			c.idle((base & 0xFF00) | (addr & 0x00FF))
		}
	}
}

// --- helpers for read/write w/ ACC mode ---
func (c *CPU) load(addr uint16, mode AddrMode) byte {
	if mode == ACC {
		return c.A
	}
	return c.read(addr)
}
func (c *CPU) store(addr uint16, mode AddrMode, v byte) {
	if mode == ACC {
		c.A = v
		return
	}
	c.write(addr, v)
}

// --- ops ---

func opLDA(c *CPU, addr uint16, m AddrMode) { c.A = c.read(addr); c.setZN(c.A) }
func opLDX(c *CPU, addr uint16, m AddrMode) { c.X = c.read(addr); c.setZN(c.X) }
func opLDY(c *CPU, addr uint16, m AddrMode) { c.Y = c.read(addr); c.setZN(c.Y) }

func opSTA(c *CPU, addr uint16, m AddrMode) { c.write(addr, c.A) }
func opSTX(c *CPU, addr uint16, m AddrMode) { c.write(addr, c.X) }
func opSTY(c *CPU, addr uint16, m AddrMode) { c.write(addr, c.Y) }

func opTAX(c *CPU, _ uint16, _ AddrMode) { c.X = c.A; c.setZN(c.X) }
func opTAY(c *CPU, _ uint16, _ AddrMode) { c.Y = c.A; c.setZN(c.Y) }
func opTSX(c *CPU, _ uint16, _ AddrMode) { c.X = c.SP; c.setZN(c.X) }
func opTXA(c *CPU, _ uint16, _ AddrMode) { c.A = c.X; c.setZN(c.A) }
func opTXS(c *CPU, _ uint16, _ AddrMode) { c.SP = c.X }
func opTYA(c *CPU, _ uint16, _ AddrMode) { c.A = c.Y; c.setZN(c.A) }

// Push/pull carry an internal dummy cycle (or two) beyond the explicit
// stack access; idle() ticks them for the per-cycle NES path (#342) and
// is a no-op elsewhere.
func opPHA(c *CPU, _ uint16, _ AddrMode) { c.idle(c.PC); c.push(c.A) }
func opPHP(c *CPU, _ uint16, _ AddrMode) { c.idle(c.PC); c.push(c.P | FlagB | FlagU) }
func opPLA(c *CPU, _ uint16, _ AddrMode) {
	c.idle(c.PC)
	c.idle(0x100 | uint16(c.SP))
	c.A = c.pop()
	c.setZN(c.A)
}
func opPLP(c *CPU, _ uint16, _ AddrMode) {
	c.idle(c.PC)
	c.idle(0x100 | uint16(c.SP))
	c.P = (c.pop() &^ FlagB) | FlagU
}

func opAND(c *CPU, addr uint16, m AddrMode) { c.A &= c.read(addr); c.setZN(c.A) }
func opEOR(c *CPU, addr uint16, m AddrMode) { c.A ^= c.read(addr); c.setZN(c.A) }
func opORA(c *CPU, addr uint16, m AddrMode) { c.A |= c.read(addr); c.setZN(c.A) }

func opBIT(c *CPU, addr uint16, m AddrMode) {
	v := c.read(addr)
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
	v := c.read(addr)
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	// Ricoh 2A03 (VariantNES): FlagD toggles but has no effect on ADC.
	// Skip the decimal path entirely.
	if c.hasFlag(FlagD) && c.Variant != VariantNES {
		if c.Variant == VariantCMOS65C02 {
			adcDecimalCMOS(c, v, carry)
		} else {
			adcDecimal(c, v, carry)
		}
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
	v := c.read(addr)
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	// Same VariantNES no-BCD rule as opADC.
	if c.hasFlag(FlagD) && c.Variant != VariantNES {
		if c.Variant == VariantCMOS65C02 {
			sbcDecimalCMOS(c, v, carry)
		} else {
			sbcDecimal(c, v, carry)
		}
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
func opCMP(c *CPU, addr uint16, m AddrMode) { cmp(c, c.A, c.read(addr)) }
func opCPX(c *CPU, addr uint16, m AddrMode) { cmp(c, c.X, c.read(addr)) }
func opCPY(c *CPU, addr uint16, m AddrMode) { cmp(c, c.Y, c.read(addr)) }

// rmwDummy is the read-modify-write internal cycle: the 6502 writes the
// unmodified value back before the modified one (memory modes only). The
// ticked dummy write keeps the per-cycle count exact (#342); a no-op
// write under non-NES variants is harmless.
func (c *CPU) rmwDummy(addr uint16, m AddrMode, old byte) {
	if m != ACC {
		c.write(addr, old)
	}
}

func opINC(c *CPU, addr uint16, m AddrMode) {
	old := c.read(addr)
	c.rmwDummy(addr, m, old)
	v := old + 1
	c.write(addr, v)
	c.setZN(v)
}
func opDEC(c *CPU, addr uint16, m AddrMode) {
	old := c.read(addr)
	c.rmwDummy(addr, m, old)
	v := old - 1
	c.write(addr, v)
	c.setZN(v)
}
func opINX(c *CPU, _ uint16, _ AddrMode) { c.X++; c.setZN(c.X) }
func opINY(c *CPU, _ uint16, _ AddrMode) { c.Y++; c.setZN(c.Y) }
func opDEX(c *CPU, _ uint16, _ AddrMode) { c.X--; c.setZN(c.X) }
func opDEY(c *CPU, _ uint16, _ AddrMode) { c.Y--; c.setZN(c.Y) }

func opASL(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	c.rmwDummy(addr, m, v)
	c.setFlag(FlagC, v&0x80 != 0)
	v <<= 1
	c.store(addr, m, v)
	c.setZN(v)
}
func opLSR(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	c.rmwDummy(addr, m, v)
	c.setFlag(FlagC, v&1 != 0)
	v >>= 1
	c.store(addr, m, v)
	c.setZN(v)
}
func opROL(c *CPU, addr uint16, m AddrMode) {
	v := c.load(addr, m)
	c.rmwDummy(addr, m, v)
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
	c.rmwDummy(addr, m, v)
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
	c.idle(0x100 | uint16(c.SP)) // internal stack-pointer cycle
	c.push16(c.PC - 1)
	c.PC = addr
}
func opRTS(c *CPU, _ uint16, _ AddrMode) {
	c.idle(c.PC) // dummy read of next byte
	c.idle(0x100 | uint16(c.SP))
	c.PC = c.pop16() + 1
	c.idle(c.PC) // internal cycle: PC increment
}
func opRTI(c *CPU, _ uint16, _ AddrMode) {
	c.idle(c.PC)                 // dummy read of next byte
	c.idle(0x100 | uint16(c.SP)) // dummy stack read before the pulls
	c.P = (c.pop() &^ FlagB) | FlagU
	c.PC = c.pop16()
}

func branch(c *CPU, addr uint16, take bool) {
	if !take {
		return
	}
	// Taken branch: +1 cycle (dummy opcode-fetch at the current PC),
	// +1 more if the target is on a different page.
	c.extraCycles++
	c.idle(c.PC)
	if (c.PC & 0xFF00) != (addr & 0xFF00) {
		c.extraCycles++
		c.idle(addr)
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
	c.idle(c.PC) // dummy read of the padding byte
	c.PC++
	c.push16(c.PC)
	// Interrupt vector hijacking (#369, cpu_interrupts_v2 nmi_and_brk):
	// the NMI poll for BRK runs at cycle 4 — after pushing PC, before
	// pushing P. If NMI is pending by then, the NMI vector is used and
	// the BRK is consumed by the NMI service.
	vec := uint16(VecIRQ)
	if c.Variant == VariantNES && c.nmiPending {
		vec = VecNMI
		c.nmiPending = false
		c.nmiDue = false
		c.nmiPollPrev = false
	}
	c.push(c.P | FlagB | FlagU)
	c.setFlag(FlagI, true)
	// CMOS quirk: BRK also clears the D flag (the running CPU's flag,
	// AFTER the pre-clear value has already been pushed). NMOS leaves
	// D alone — a real 6502 bug Klaus's test specifically checks for.
	if c.Variant == VariantCMOS65C02 {
		c.setFlag(FlagD, false)
	}
	lo := uint16(c.read(vec))
	hi := uint16(c.read(vec + 1))
	c.PC = lo | hi<<8
	// 6502 quirk: the handler's first instruction must run before a
	// pending NMI takes (#369). Clear the poll latches; nmiPending
	// stays so the next instruction re-establishes nmiDue.
	if c.Variant == VariantNES {
		c.nmiDue = false
		c.nmiPollPrev = false
	}
}
func opNOP(c *CPU, addr uint16, m AddrMode) {
	// Illegal multi-byte NOPs (DOP/TOP) read their operand and discard
	// it — that read is a real cycle. The canonical implied NOP (and
	// accumulator forms) have no operand; addrDummies ticks their idle.
	if m != IMP && m != ACC {
		_ = c.read(addr)
	}
}

// opWAI (CMOS $CB) puts the CPU to sleep until an IRQ or NMI is signalled.
// Once awakened, execution continues with the next opcode; the interrupt
// handler (if any) runs first via the normal service path on the next
// Step(). Wake fires even when FlagI masks the IRQ — see Step()'s
// WAI-wake-on-masked-IRQ block.
func opWAI(c *CPU, _ uint16, _ AddrMode) { c.Halted = true }

// opSTP (CMOS $DB) halts the CPU permanently; only Reset can clear the
// stoppedBySTP latch. Interrupts are ignored while in STP-halt.
func opSTP(c *CPU, _ uint16, _ AddrMode) {
	c.Halted = true
	c.stoppedBySTP = true
}
