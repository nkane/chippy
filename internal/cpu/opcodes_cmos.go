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
	set(0x80, "BRA", REL, 2, 3, false, opBRA)

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
		set(byte(0x07+bit*0x10), "RMB", ZP, 2, 5, false, func(c *CPU, addr uint16, _ AddrMode) {
			c.Bus.Write(addr, c.Bus.Read(addr)&^(1<<b))
		})
		set(byte(0x87+bit*0x10), "SMB", ZP, 2, 5, false, func(c *CPU, addr uint16, _ AddrMode) {
			c.Bus.Write(addr, c.Bus.Read(addr)|(1<<b))
		})
		set(byte(0x0F+bit*0x10), "BBR", ZPR, 3, 5, false, func(c *CPU, _ uint16, _ AddrMode) {
			zp := c.Bus.Read(c.PC)
			off := int8(c.Bus.Read(c.PC + 1))
			c.PC += 2
			target := uint16(int32(c.PC) + int32(off))
			if c.Bus.Read(uint16(zp))&(1<<b) == 0 {
				c.extraCycles++
				if (c.PC & 0xFF00) != (target & 0xFF00) {
					c.extraCycles++
				}
				c.PC = target
			}
		})
		set(byte(0x8F+bit*0x10), "BBS", ZPR, 3, 5, false, func(c *CPU, _ uint16, _ AddrMode) {
			zp := c.Bus.Read(c.PC)
			off := int8(c.Bus.Read(c.PC + 1))
			c.PC += 2
			target := uint16(int32(c.PC) + int32(off))
			if c.Bus.Read(uint16(zp))&(1<<b) != 0 {
				c.extraCycles++
				if (c.PC & 0xFF00) != (target & 0xFF00) {
					c.extraCycles++
				}
				c.PC = target
			}
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
			// $44 is the only undefined $x4 slot — 2-byte/3-cycle ZP
			// NOP per WDC silicon. Klaus's `db $44` test at $0DCC
			// requires exactly this width.
			set(byte(op), "NOP", ZP, 2, 3, false, opNOP2)
		case 0x0C:
			// $5C is the WDC-quirky 3-byte/8-cycle slot. $DC + $FC are
			// the only other "???" $xC slots; both are 3-byte/4-cycle
			// ABS NOPs (+1 on page cross).
			if op == 0x5C {
				set(byte(op), "NOP", ABS, 3, 8, false, opNOP3)
			} else {
				set(byte(op), "NOP", ABS, 3, 4, true, opNOP3)
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

func opPHX(c *CPU, _ uint16, _ AddrMode) { c.push(c.X) }
func opPHY(c *CPU, _ uint16, _ AddrMode) { c.push(c.Y) }
func opPLX(c *CPU, _ uint16, _ AddrMode) { c.X = c.pop(); c.setZN(c.X) }
func opPLY(c *CPU, _ uint16, _ AddrMode) { c.Y = c.pop(); c.setZN(c.Y) }

func opSTZ(c *CPU, addr uint16, _ AddrMode) { c.Bus.Write(addr, 0) }

func opTRB(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	c.setFlag(FlagZ, v&c.A == 0)
	c.Bus.Write(addr, v&^c.A)
}
func opTSB(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	c.setFlag(FlagZ, v&c.A == 0)
	c.Bus.Write(addr, v|c.A)
}

func opINA(c *CPU, _ uint16, _ AddrMode) { c.A++; c.setZN(c.A) }
func opDEA(c *CPU, _ uint16, _ AddrMode) { c.A--; c.setZN(c.A) }

// BIT immediate on 65C02 only updates Z (does NOT touch N or V).
func opBITimm(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
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
	if res >= 0xA0 {
		res += 0x60
	}
	c.setFlag(FlagC, res >= 0x100)
	c.setFlag(FlagV, ((a^res)&^(a^uint16(v)))&0x80 != 0)
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
