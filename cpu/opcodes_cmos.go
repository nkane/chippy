package cpu

// 65C02 (WDC/Rockwell CMOS) opcode table and handlers.
//
// References:
//   - http://www.6502.org/tutorials/65c02opcodes.html
//   - http://wilsonminesco.com/NMOS-CMOSdif/
//   - http://www.oxyron.de/html/opcodesc02.html

// OpcodesCMOS is the 65C02 opcode table. See Opcodes for the NMOS table.
var OpcodesCMOS [256]Instr

func init() {
	// Start from the NMOS table so all official 6502 opcodes are inherited.
	// Go guarantees package-level vars (including the NMOS Opcodes table)
	// are fully initialised before any init() runs, and orders init() calls
	// across files alphabetically. opcodes.go runs before opcodes_cmos.go
	// (lexicographic) so Opcodes[] is already populated by the time we
	// copy from it.
	for i := range OpcodesCMOS {
		OpcodesCMOS[i] = Opcodes[i]
	}

	set := func(op byte, name string, mode AddrMode, bytes, cycles int, pageAdd bool, fn func(*CPU, uint16, AddrMode)) {
		OpcodesCMOS[op] = Instr{name, mode, bytes, cycles, pageAdd, fn}
	}

	// --- New CMOS instructions ---

	// BRA — Branch Always (rel)
	// BRA base = 2; branch() adds +1 for always-taken + maybe +1 for
	// page-cross, matching the 3/4 cycle spec for taken-no-cross / taken-cross.
	set(0x80, "BRA", REL, 2, 2, false, opBRA)

	// Stack ops for X/Y
	set(0xDA, "PHX", IMP, 1, 3, false, opPHX)
	set(0x5A, "PHY", IMP, 1, 3, false, opPHY)
	set(0xFA, "PLX", IMP, 1, 4, false, opPLX)
	set(0x7A, "PLY", IMP, 1, 4, false, opPLY)

	// STZ — Store Zero
	set(0x64, "STZ", ZP, 2, 3, false, opSTZ)
	set(0x74, "STZ", ZPX, 2, 4, false, opSTZ)
	set(0x9C, "STZ", ABS, 3, 4, false, opSTZ)
	set(0x9E, "STZ", ABX, 3, 5, false, opSTZ)

	// TRB / TSB — Test and Reset/Set Bits
	set(0x14, "TRB", ZP, 2, 5, false, opTRB)
	set(0x1C, "TRB", ABS, 3, 6, false, opTRB)
	set(0x04, "TSB", ZP, 2, 5, false, opTSB)
	set(0x0C, "TSB", ABS, 3, 6, false, opTSB)

	// INA / DEA — Increment/Decrement A
	set(0x1A, "INC", ACC, 1, 2, false, opINA)
	set(0x3A, "DEC", ACC, 1, 2, false, opDEA)

	// JMP (abs,X)
	set(0x7C, "JMP", IAX, 3, 6, false, opJMP)

	// JMP (abs) — 65C02 takes 6 cycles (NMOS 5) and fixes the page-wrap bug
	// (handled per-variant in resolve(IND)).
	set(0x6C, "JMP", IND, 3, 6, false, opJMP)

	// 65C02 optimized the absolute,X read-modify-write SHIFTS/ROTATES to 6
	// cycles, +1 only when the index crosses a page (NMOS always took 7).
	// INC/DEC abs,X ($DE/$FE) did NOT get this and stay 7, so they are left
	// as inherited from the NMOS table.
	set(0x1E, "ASL", ABX, 3, 6, true, opASL)
	set(0x3E, "ROL", ABX, 3, 6, true, opROL)
	set(0x5E, "LSR", ABX, 3, 6, true, opLSR)
	set(0x7E, "ROR", ABX, 3, 6, true, opROR)

	// New addressing modes for existing ops:
	// BIT immediate / ZPX / ABX
	set(0x89, "BIT", IMM, 2, 2, false, opBITimm)
	set(0x34, "BIT", ZPX, 2, 4, false, opBIT)
	set(0x3C, "BIT", ABX, 3, 4, true, opBIT)

	// (zp) variants — opcode pattern: family low nibble = 2, high nibble varies
	set(0x12, "ORA", IZP, 2, 5, false, opORA)
	set(0x32, "AND", IZP, 2, 5, false, opAND)
	set(0x52, "EOR", IZP, 2, 5, false, opEOR)
	set(0x72, "ADC", IZP, 2, 5, false, opADC)
	set(0x92, "STA", IZP, 2, 5, false, opSTA)
	set(0xB2, "LDA", IZP, 2, 5, false, opLDA)
	set(0xD2, "CMP", IZP, 2, 5, false, opCMP)
	set(0xF2, "SBC", IZP, 2, 5, false, opSBC)

	// Rockwell bit ops: RMB0..7, SMB0..7, BBR0..7, BBS0..7
	for bit := 0; bit < 8; bit++ {
		b := bit
		set(byte(0x07+bit*0x10), "RMB", ZP, 2, 5, false, func(c *CPU, addr uint16, m AddrMode) {
			v := c.read(addr)
			c.rmwDummy(addr, m, v) // 65C02 RMW dummy read (issue #455)
			c.write(addr, v&^(1<<b))
		})
		set(byte(0x87+bit*0x10), "SMB", ZP, 2, 5, false, func(c *CPU, addr uint16, m AddrMode) {
			v := c.read(addr)
			c.rmwDummy(addr, m, v) // 65C02 RMW dummy read (issue #455)
			c.write(addr, v|(1<<b))
		})
		// BBR/BBS take a flat 6 cycles on real 65C02 silicon (Tom Harte
		// wdc65c02) — no taken / page-cross penalty, unlike a normal branch.
		set(byte(0x0F+bit*0x10), "BBR", ZPR, 3, 6, false, func(c *CPU, _ uint16, _ AddrMode) {
			branchBitTest(c, byte(b), false) // BBR: branch when the bit is reset
		})
		set(byte(0x8F+bit*0x10), "BBS", ZPR, 3, 6, false, func(c *CPU, _ uint16, _ AddrMode) {
			branchBitTest(c, byte(b), true) // BBS: branch when the bit is set
		})
	}

	// CMOS quirk: replace any remaining NMOS illegals with WDC-spec NOPs.
	// Per WDC datasheet, all otherwise-undefined CMOS opcodes act as NOPs;
	// many are 1-byte/1-cycle, the rest follow specific multi-byte/cycle
	// patterns. We use the standard table from 6502.org.
	cmosNOPs(set)

	// WDC 65C02 halt opcodes (Rockwell omits them; chippy implements WDC).
	//   WAI ($CB) halts until any IRQ/NMI; service runs on wake.
	//   STP ($DB) halts until external Reset; interrupts ignored while stopped.
	set(0xCB, "WAI", IMP, 1, 3, false, opWAI)
	set(0xDB, "STP", IMP, 1, 3, false, opSTP)
}

// cmosNOPs installs the WDC-spec NOPs that replace all NMOS undefined opcodes
// not already overridden above.
func cmosNOPs(set func(op byte, name string, mode AddrMode, bytes, cycles int, pageAdd bool, fn func(*CPU, uint16, AddrMode))) {
	// Per http://www.6502.org/tutorials/65c02opcodes.html
	// The unimplemented opcodes are NOPs. Their byte-count and cycle-count
	// are determined by the low nibble:
	//   x2 (except 02): 2-byte/2-cycle (handled above as IZP variants)
	//   x3, xB: 1-byte/1-cycle
	//   x7, xF: handled above (RMB/SMB/BBR/BBS)
	//   xC: 3-byte/8-cycle (only $5C used; others overridden)
	//   xA: 1-byte/1-cycle when not assigned
	// We iterate and patch opcodes whose Name is still "???".
	for op := 0; op < 256; op++ {
		if OpcodesCMOS[op].Name != "???" {
			continue
		}
		lo := op & 0x0F
		switch lo {
		case 0x02:
			// $x2 slots not used as IZP ops above act as 2-byte/2-cycle
			// IMM NOPs (load + discard).
			set(byte(op), "NOP", IMM, 2, 2, false, opNOP2)
		case 0x03, 0x0B:
			// $x3 and $xB are all 1-byte/1-cycle NOPs per WDC datasheet.
			set(byte(op), "NOP", IMP, 1, 1, false, opNOP)
		case 0x04:
			// $x4 splits two ways: $44 is the documented 2-byte/3-cycle
			// ZP NOP (per WDC + Klaus's `db $44` at $0DCC); $54 / $D4 /
			// $F4 are 2-byte/4-cycle ZPX NOPs (the ZPX-prefix family).
			// $14, $34, $74 are TRB / BIT / BIT and override "???" so
			// they don't reach this branch.
			if op == 0x44 {
				set(byte(op), "NOP", ZP, 2, 3, false, opNOP2)
			} else {
				set(byte(op), "NOP", ZPX, 2, 4, false, opNOP2)
			}
		case 0x0C:
			// $5C is the WDC-quirky 3-byte/8-cycle slot. $DC + $FC are
			// the only other "???" $xC slots; both are 3-byte/4-cycle
			// ABS NOPs (+1 on page cross).
			if op == 0x5C {
				// WDC $5C: 3-byte, 4-cycle NOP (Tom Harte wdc65c02). The
				// often-cited "8 cycles" is the W65C816 figure; the real
				// W65C02S takes 4.
				set(byte(op), "NOP", ABS, 3, 4, false, opNOPAbs65C02)
			} else {
				set(byte(op), "NOP", ABS, 3, 4, false, opNOPAbs65C02)
			}
		default:
			// $54, $D4, $F4 reach here (low nibble 4 above only
			// matched $44). They're 2-byte/4-cycle ZPX NOPs.
			if lo == 0x04 || op == 0x54 || op == 0xD4 || op == 0xF4 {
				set(byte(op), "NOP", ZPX, 2, 4, false, opNOP2)
				continue
			}
			// All other unimplemented: 1-byte/1-cycle NOP.
			set(byte(op), "NOP", IMP, 1, 1, false, opNOP)
		}
	}
}

// --- New op handlers ---

func opBRA(c *CPU, addr uint16, _ AddrMode) { branch(c, addr, true) }

// branchBitTest implements the 65C02 BBR/BBS per-cycle bus sequence (issue
// #475), a flat 6 cycles after the opcode fetch: read the zero-page address
// operand, read the zero-page byte (bit test), a dummy write-back of that byte
// (the 65C02 RMW-style cycle), read the relative operand, then a dummy read of
// the computed branch target — which happens ALWAYS, even when the branch is
// not taken. The branch is taken when the tested bit equals branchIfSet (BBS
// branches on set, BBR on reset). PC sits at the zp operand on entry (ZPR
// resolve leaves it there).
func branchBitTest(c *CPU, bit byte, branchIfSet bool) {
	zp := c.read(c.PC)
	c.PC++
	v := c.read(uint16(zp))
	c.write(uint16(zp), v) // dummy write-back (same value)
	off := int8(c.read(c.PC))
	c.PC++
	target := uint16(int32(c.PC) + int32(off))
	// The dummy target read uses the un-fixed address (old high byte); the
	// page-cross carry only reaches PC when the branch is actually taken.
	c.read((c.PC & 0xFF00) | (target & 0x00FF))
	if (v&(1<<bit) != 0) == branchIfSet {
		c.PC = target
	}
}

// opNOPAbs65C02 handles the WDC 3-byte NOPs ($5C/$DC/$FC): they do NOT
// dereference the operand (unlike the NMOS illegal TOP NOPs); the 4th cycle is
// a dummy re-read of the high operand byte at c.PC-1 (issue #455).
func opNOPAbs65C02(c *CPU, _ uint16, _ AddrMode) { c.idle(c.PC - 1) }

// Push/pull carry the same internal dummy cycles as PHA/PLA — idle() ticks
// them for the per-cycle path (issue #455); a no-op elsewhere.
func opPHX(c *CPU, _ uint16, _ AddrMode) { c.idle(c.PC); c.push(c.X) }
func opPHY(c *CPU, _ uint16, _ AddrMode) { c.idle(c.PC); c.push(c.Y) }
func opPLX(c *CPU, _ uint16, _ AddrMode) {
	c.idle(c.PC)
	c.idle(0x100 | uint16(c.SP))
	c.X = c.pop()
	c.setZN(c.X)
}
func opPLY(c *CPU, _ uint16, _ AddrMode) {
	c.idle(c.PC)
	c.idle(0x100 | uint16(c.SP))
	c.Y = c.pop()
	c.setZN(c.Y)
}

func opSTZ(c *CPU, addr uint16, _ AddrMode) { c.write(addr, 0) }

func opTRB(c *CPU, addr uint16, m AddrMode) {
	v := c.read(addr)
	c.rmwDummy(addr, m, v) // 65C02 RMW dummy read (issue #455)
	c.setFlag(FlagZ, v&c.A == 0)
	c.write(addr, v&^c.A)
}
func opTSB(c *CPU, addr uint16, m AddrMode) {
	v := c.read(addr)
	c.rmwDummy(addr, m, v) // 65C02 RMW dummy read (issue #455)
	c.setFlag(FlagZ, v&c.A == 0)
	c.write(addr, v|c.A)
}

func opINA(c *CPU, _ uint16, _ AddrMode) { c.A++; c.setZN(c.A) }
func opDEA(c *CPU, _ uint16, _ AddrMode) { c.A--; c.setZN(c.A) }

// BIT immediate on 65C02 only updates Z (does NOT touch N or V).
func opBITimm(c *CPU, addr uint16, _ AddrMode) {
	v := c.read(addr)
	c.setFlag(FlagZ, c.A&v == 0)
}

// adcDecimalCMOS performs the 65C02 packed-BCD ADC. Algorithm matches
// Bruce Clark's reference (https://6502.org/tutorials/decimal_mode.html
// Appendix B): low-nibble carry both bumps the nibble AND masks the
// result, so invalid BCD inputs ($0A..$0F low nibble) produce the same
// answer as real WDC silicon. CMOS-specific tweaks vs. NMOS: N and Z
// reflect the decimal result; +1 cycle penalty.
func adcDecimalCMOS(c *CPU, v byte, carry uint16) {
	a := uint16(c.A)
	al := (a & 0x0F) + (uint16(v) & 0x0F) + carry
	if al >= 0x0A {
		al = ((al + 0x06) & 0x0F) + 0x10
	}
	res := (a & 0xF0) + (uint16(v) & 0xF0) + al
	// V is set from the result BEFORE the high-nibble decimal correction
	// (the partial binary-ish sum), matching silicon; N/Z come from the final
	// decimal result on CMOS.
	c.setFlag(FlagV, ((a^res)&^(a^uint16(v)))&0x80 != 0)
	if res >= 0xA0 {
		res += 0x60
	}
	c.setFlag(FlagC, res >= 0x100)
	c.A = byte(res & 0xFF)
	c.setZN(c.A)
	c.extraCycles++ // CMOS BCD takes one extra cycle
}

// sbcDecimalCMOS performs the 65C02 packed-BCD SBC. Mirrors the
// Appendix B reference: low-nibble underflow both shifts the nibble
// AND masks the result, so invalid-BCD inputs match real silicon.
// CMOS: N and Z come from the decimal result; V is documented
// undefined but we report the binary-path V (matches NMOS and our
// NMOS tests).
func sbcDecimalCMOS(c *CPU, v byte, carry uint16) {
	a := int(c.A)
	vi := int(v)
	cin := int(carry)
	al := (a & 0x0F) - (vi & 0x0F) + cin - 1
	if al < 0 {
		al = ((al - 0x06) & 0x0F) - 0x10
	}
	res := (a & 0xF0) - (vi & 0xF0) + al
	if res < 0 {
		res -= 0x60
	}
	// Flags from binary subtract.
	w := v ^ 0xFF
	sum := uint16(c.A) + uint16(w) + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^uint16(w))&(uint16(c.A)^sum))&0x80 != 0)
	c.A = byte(res & 0xFF)
	c.setZN(c.A) // CMOS: N/Z reflect decimal result
	c.extraCycles++
}
