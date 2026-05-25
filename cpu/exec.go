package cpu

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
	if c.irqLine && !c.hasFlag(FlagI) {
		c.Halted = false
		if c.Tracer != nil {
			c.Tracer.LogInterrupt(c, "IRQ", VecIRQ)
		}
		before := c.Cycles
		c.serviceIRQ()
		return int(c.Cycles - before)
	}

	// Drain any pending bus-stealing stall (e.g. $4014 OAMDMA) before
	// fetching the next opcode. The whole counter is consumed as one
	// block — the PPU / APU sees its cycle delta via the ticker but
	// no opcode runs this Step. Interrupts handled above intentionally
	// pre-empt the drain: a peripheral that asserts NMI mid-DMA gets
	// serviced first, matching the "next instruction boundary" model
	// the rest of the interrupt path already uses.
	if c.pendingStall > 0 {
		stalled := c.pendingStall
		c.pendingStall = 0
		c.Cycles += uint64(stalled)
		if c.busTicker != nil {
			c.busTicker.Tick(stalled)
		}
		return stalled
	}

	if c.Halted {
		return 0
	}
	if c.Tracer != nil {
		c.Tracer.LogStep(c, c.Bus)
	}
	startPC := c.PC
	op := c.Bus.Read(c.PC)
	c.PC++
	in := c.opcodes[op]
	addr, pageCrossed := c.resolve(in.Mode)
	c.extraCycles = 0
	// Per-cycle PPU sync for the NES variant (#342). The 2C02 needs a
	// $2002 read to sample the PPU at the instruction's data-access
	// cycle — the operand read/write lands on the final cycle for the
	// load/store + RMW modes that reach PPU registers. So advance the
	// bus ticker by the base instruction length BEFORE running the
	// opcode body; the branch + page-cross extras (which never touch
	// $2000/$2002) tick afterward. Other CPU variants keep the simpler
	// post-instruction batch tick — the chippy debugger doesn't drive
	// a dot-accurate PPU, so its byte-for-byte behavior is unchanged.
	//
	// Together with the NMI interrupt-poll latch (nmiDue, sampled at the
	// penultimate cycle just below) this passes Blargg ppu_vbl_nmi tests
	// 2-5 (vbl_set_time, vbl_clear_time, nmi_control, nmi_timing). The
	// remaining sub-tests (6+: suppression) need a $2002 read to race the
	// /NMI edge at sub-cycle resolution — the read must land *between* the
	// PPU's flag-set and the CPU's edge-sample — which this instruction-
	// stepped, pre-tick model can't represent. That needs true per-cycle
	// CPU↔PPU interleave; still scoped to #342.
	nesSync := c.Variant == VariantNES && c.busTicker != nil
	switch {
	case nesSync:
		// Advance the PPU to the penultimate cycle, poll the NMI line,
		// then tick the final cycle. The 6502 samples interrupts before
		// the last cycle, so an NMI raised on that cycle (or by the
		// opcode body, the final-cycle bus operation) is serviced one
		// instruction later — Blargg 05-nmi_timing pins this. The full
		// base length still ticks before the body, so a $2002 read in the
		// body samples the PPU at its true data-access dot (#342).
		if in.Cycles > 1 {
			c.busTicker.Tick(in.Cycles - 1)
		}
		c.nmiDue = c.nmiPending
		c.busTicker.Tick(1)
	case c.Variant == VariantNES:
		// NES without a bus ticker still polls so the nmiDue latch
		// advances and NMIs get serviced.
		c.nmiDue = c.nmiPending
	}
	in.Exec(c, addr, in.Mode)
	cycles := in.Cycles + c.extraCycles
	if in.PageAdd && pageCrossed {
		cycles++
	}
	c.Cycles += uint64(cycles)
	if nesSync {
		if rem := cycles - in.Cycles; rem > 0 {
			c.busTicker.Tick(rem)
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
	v := c.Bus.Read(addr)
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
	c.extraCycles++
	if (c.PC & 0xFF00) != (addr & 0xFF00) {
		c.extraCycles++
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
	// CMOS quirk: BRK also clears the D flag (the running CPU's flag,
	// AFTER the pre-clear value has already been pushed). NMOS leaves
	// D alone — a real 6502 bug Klaus's test specifically checks for.
	if c.Variant == VariantCMOS65C02 {
		c.setFlag(FlagD, false)
	}
	lo := uint16(c.Bus.Read(VecIRQ))
	hi := uint16(c.Bus.Read(VecIRQ + 1))
	c.PC = lo | hi<<8
}
func opNOP(c *CPU, _ uint16, _ AddrMode) {}

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
