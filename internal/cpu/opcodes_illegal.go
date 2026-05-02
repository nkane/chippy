// Package cpu — undocumented ("illegal") NMOS 6502 opcodes.
//
// Implements the stable subset that real silicon executes deterministically.
// Behavior references:
//
//   - http://www.oxyron.de/html/opcodes02.html
//   - https://www.masswerk.at/6502/6502_instruction_set.html
//
// Implemented:
//   LAX  — LDA + LDX (no flag-set surprises beyond setZN)
//   SAX  — store (A & X), no flag changes
//   DCP  — DEC then CMP
//   ISC  — INC then SBC (also called ISB)
//   SLO  — ASL then ORA
//   RLA  — ROL then AND
//   SRE  — LSR then EOR
//   RRA  — ROR then ADC (decimal-aware via opADC's D-mode branch)
//   ANC  — AND #imm; C := N (i.e. C := bit7 of result)
//   ALR  — AND #imm then LSR A
//   ARR  — AND #imm then ROR A; sets V and C from the result's bits 5/6
//   SBX  — X := (A & X) - imm  (carry set if no borrow; flags from result)
//   NOPs — multi-byte / multi-cycle no-ops at undocumented opcode slots
//
// Unstable opcodes (silicon-dependent on bus capacitance / hi-byte+1 ANDs)
// remain decoded as plain NOPs:
//   AHX/SHA, SHY, SHX, TAS/SHS, LAS/LAR, XAA/ANE, KIL/JAM
//
// These cannot be reliably emulated and no widely-used software depends on
// them. They occupy 1 byte by default and consume 2 cycles, which matches
// the placeholder fill in opcodes.go's init().
package cpu

// --- combined R-M-W illegals ---

// opDCP: M := M-1; CMP A vs new M.
func opDCP(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr) - 1
	c.Bus.Write(addr, v)
	cmp(c, c.A, v)
}

// opISC: M := M+1; SBC A, new M. Honors decimal mode via opSBC path.
func opISC(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr) + 1
	c.Bus.Write(addr, v)
	// Inline SBC against v: re-use the same arithmetic as opSBC. We can't
	// call opSBC directly (it would re-read), so duplicate the dispatch.
	carry := uint16(0)
	if c.hasFlag(FlagC) {
		carry = 1
	}
	if c.hasFlag(FlagD) {
		sbcDecimal(c, v, carry)
		return
	}
	w := uint16(v) ^ 0xFF
	sum := uint16(c.A) + w + carry
	c.setFlag(FlagC, sum > 0xFF)
	c.setFlag(FlagV, (^(uint16(c.A)^w)&(uint16(c.A)^sum))&0x80 != 0)
	c.A = byte(sum)
	c.setZN(c.A)
}

// opSLO: M := M<<1; A := A | M. C := old bit 7.
func opSLO(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	c.setFlag(FlagC, v&0x80 != 0)
	v <<= 1
	c.Bus.Write(addr, v)
	c.A |= v
	c.setZN(c.A)
}

// opRLA: M := ROL M; A := A & M.
func opRLA(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	carryIn := byte(0)
	if c.hasFlag(FlagC) {
		carryIn = 1
	}
	c.setFlag(FlagC, v&0x80 != 0)
	v = (v << 1) | carryIn
	c.Bus.Write(addr, v)
	c.A &= v
	c.setZN(c.A)
}

// opSRE: M := M>>1; A := A ^ M. C := old bit 0.
func opSRE(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	c.setFlag(FlagC, v&1 != 0)
	v >>= 1
	c.Bus.Write(addr, v)
	c.A ^= v
	c.setZN(c.A)
}

// opRRA: M := ROR M; ADC A, new M. Honors decimal mode via opADC path.
func opRRA(c *CPU, addr uint16, m AddrMode) {
	v := c.Bus.Read(addr)
	carryIn := byte(0)
	if c.hasFlag(FlagC) {
		carryIn = 0x80
	}
	c.setFlag(FlagC, v&1 != 0)
	v = (v >> 1) | carryIn
	c.Bus.Write(addr, v)
	// ADC with v
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

// --- load/store illegals ---

// opLAX: A,X := M.
func opLAX(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	c.A = v
	c.X = v
	c.setZN(v)
}

// opSAX: M := A & X. Does not affect any flags.
func opSAX(c *CPU, addr uint16, _ AddrMode) {
	c.Bus.Write(addr, c.A&c.X)
}

// --- immediate-only illegals ---

// opANC: A := A & imm; then C := bit7 of A (i.e. C copies N).
func opANC(c *CPU, addr uint16, _ AddrMode) {
	c.A &= c.Bus.Read(addr)
	c.setZN(c.A)
	c.setFlag(FlagC, c.A&0x80 != 0)
}

// opALR (also ASR): A := (A & imm) >> 1. C := old bit 0 of (A & imm).
func opALR(c *CPU, addr uint16, _ AddrMode) {
	c.A &= c.Bus.Read(addr)
	c.setFlag(FlagC, c.A&1 != 0)
	c.A >>= 1
	c.setZN(c.A)
}

// opARR: A := ROR(A & imm). Then a C/V quirk:
//   C := bit 6 of result
//   V := bit 6 XOR bit 5 of result
// (Decimal-mode ARR has even weirder behaviour; we implement the binary
// case here. Real software almost never uses ARR in decimal mode.)
func opARR(c *CPU, addr uint16, _ AddrMode) {
	c.A &= c.Bus.Read(addr)
	carryIn := byte(0)
	if c.hasFlag(FlagC) {
		carryIn = 0x80
	}
	c.A = (c.A >> 1) | carryIn
	c.setZN(c.A)
	c.setFlag(FlagC, c.A&0x40 != 0)
	c.setFlag(FlagV, ((c.A>>6)^(c.A>>5))&1 != 0)
}

// opSBX (also called AXS): X := (A & X) - imm. Carry set if no borrow.
// A is unchanged. Flags are set from the result.
func opSBX(c *CPU, addr uint16, _ AddrMode) {
	v := c.Bus.Read(addr)
	tmp := uint16(c.A&c.X) + uint16(v^0xFF) + 1 // i.e. (A&X) - v
	c.setFlag(FlagC, tmp > 0xFF)
	c.X = byte(tmp)
	c.setZN(c.X)
}

// opNOP2 / opNOP3 — multi-byte NOPs that still need to consume the operand
// byte (which the addressing-mode resolver already reads via `addr` param).
// We don't need a body; the side effect of having a non-IMP mode in the
// opcode table is the PC advancing past the operand. opNOP() works fine
// here because the PC bump happens in resolve(), not in Exec.
//
// We expose this alias purely for readability in the table.
func opNOP2(c *CPU, _ uint16, _ AddrMode) {}
func opNOP3(c *CPU, _ uint16, _ AddrMode) {}

// --- table installation ---

func init() {
	set := func(op byte, name string, mode AddrMode, bytes, cycles int, pageAdd bool, fn func(*CPU, uint16, AddrMode)) {
		Opcodes[op] = Instr{name, mode, bytes, cycles, pageAdd, fn}
	}

	// LAX — load A and X from memory
	set(0xA7, "LAX", ZP, 2, 3, false, opLAX)
	set(0xB7, "LAX", ZPY, 2, 4, false, opLAX)
	set(0xAF, "LAX", ABS, 3, 4, false, opLAX)
	set(0xBF, "LAX", ABY, 3, 4, true, opLAX)
	set(0xA3, "LAX", IZX, 2, 6, false, opLAX)
	set(0xB3, "LAX", IZY, 2, 5, true, opLAX)
	// $AB is "LAX #imm" (XAA-ish, unstable). Leave as NOP.

	// SAX — store A AND X
	set(0x87, "SAX", ZP, 2, 3, false, opSAX)
	set(0x97, "SAX", ZPY, 2, 4, false, opSAX)
	set(0x8F, "SAX", ABS, 3, 4, false, opSAX)
	set(0x83, "SAX", IZX, 2, 6, false, opSAX)

	// DCP — DEC then CMP
	set(0xC7, "DCP", ZP, 2, 5, false, opDCP)
	set(0xD7, "DCP", ZPX, 2, 6, false, opDCP)
	set(0xCF, "DCP", ABS, 3, 6, false, opDCP)
	set(0xDF, "DCP", ABX, 3, 7, false, opDCP)
	set(0xDB, "DCP", ABY, 3, 7, false, opDCP)
	set(0xC3, "DCP", IZX, 2, 8, false, opDCP)
	set(0xD3, "DCP", IZY, 2, 8, false, opDCP)

	// ISC — INC then SBC
	set(0xE7, "ISC", ZP, 2, 5, false, opISC)
	set(0xF7, "ISC", ZPX, 2, 6, false, opISC)
	set(0xEF, "ISC", ABS, 3, 6, false, opISC)
	set(0xFF, "ISC", ABX, 3, 7, false, opISC)
	set(0xFB, "ISC", ABY, 3, 7, false, opISC)
	set(0xE3, "ISC", IZX, 2, 8, false, opISC)
	set(0xF3, "ISC", IZY, 2, 8, false, opISC)

	// SLO — ASL then ORA
	set(0x07, "SLO", ZP, 2, 5, false, opSLO)
	set(0x17, "SLO", ZPX, 2, 6, false, opSLO)
	set(0x0F, "SLO", ABS, 3, 6, false, opSLO)
	set(0x1F, "SLO", ABX, 3, 7, false, opSLO)
	set(0x1B, "SLO", ABY, 3, 7, false, opSLO)
	set(0x03, "SLO", IZX, 2, 8, false, opSLO)
	set(0x13, "SLO", IZY, 2, 8, false, opSLO)

	// RLA — ROL then AND
	set(0x27, "RLA", ZP, 2, 5, false, opRLA)
	set(0x37, "RLA", ZPX, 2, 6, false, opRLA)
	set(0x2F, "RLA", ABS, 3, 6, false, opRLA)
	set(0x3F, "RLA", ABX, 3, 7, false, opRLA)
	set(0x3B, "RLA", ABY, 3, 7, false, opRLA)
	set(0x23, "RLA", IZX, 2, 8, false, opRLA)
	set(0x33, "RLA", IZY, 2, 8, false, opRLA)

	// SRE — LSR then EOR
	set(0x47, "SRE", ZP, 2, 5, false, opSRE)
	set(0x57, "SRE", ZPX, 2, 6, false, opSRE)
	set(0x4F, "SRE", ABS, 3, 6, false, opSRE)
	set(0x5F, "SRE", ABX, 3, 7, false, opSRE)
	set(0x5B, "SRE", ABY, 3, 7, false, opSRE)
	set(0x43, "SRE", IZX, 2, 8, false, opSRE)
	set(0x53, "SRE", IZY, 2, 8, false, opSRE)

	// RRA — ROR then ADC
	set(0x67, "RRA", ZP, 2, 5, false, opRRA)
	set(0x77, "RRA", ZPX, 2, 6, false, opRRA)
	set(0x6F, "RRA", ABS, 3, 6, false, opRRA)
	set(0x7F, "RRA", ABX, 3, 7, false, opRRA)
	set(0x7B, "RRA", ABY, 3, 7, false, opRRA)
	set(0x63, "RRA", IZX, 2, 8, false, opRRA)
	set(0x73, "RRA", IZY, 2, 8, false, opRRA)

	// Single-byte / single-operand specials
	set(0x0B, "ANC", IMM, 2, 2, false, opANC)
	set(0x2B, "ANC", IMM, 2, 2, false, opANC) // alias
	set(0x4B, "ALR", IMM, 2, 2, false, opALR)
	set(0x6B, "ARR", IMM, 2, 2, false, opARR)
	set(0xCB, "SBX", IMM, 2, 2, false, opSBX)

	// Duplicate SBC opcode at $EB (same behavior as $E9, ca65 emits this
	// for some macros). Map to opSBC with IMM mode.
	set(0xEB, "SBC", IMM, 2, 2, false, opSBC)

	// Multi-byte NOPs. These are hit by some test ROMs and (rarely) real
	// software relying on cycle padding. Cycle counts per oxyron table.
	// Single-byte 2-cycle NOPs at undocumented slots: $1A, $3A, $5A, $7A, $DA, $FA
	for _, op := range []byte{0x1A, 0x3A, 0x5A, 0x7A, 0xDA, 0xFA} {
		set(op, "NOP", IMP, 1, 2, false, opNOP)
	}
	// 2-byte NOPs: $80, $82, $89, $C2, $E2 are NOP #imm (2 cycles).
	for _, op := range []byte{0x80, 0x82, 0x89, 0xC2, 0xE2} {
		set(op, "NOP", IMM, 2, 2, false, opNOP2)
	}
	// 2-byte ZP NOPs: $04, $44, $64 (3 cycles).
	for _, op := range []byte{0x04, 0x44, 0x64} {
		set(op, "NOP", ZP, 2, 3, false, opNOP2)
	}
	// 2-byte ZPX NOPs: $14, $34, $54, $74, $D4, $F4 (4 cycles).
	for _, op := range []byte{0x14, 0x34, 0x54, 0x74, 0xD4, 0xF4} {
		set(op, "NOP", ZPX, 2, 4, false, opNOP2)
	}
	// 3-byte ABS NOP: $0C (4 cycles).
	set(0x0C, "NOP", ABS, 3, 4, false, opNOP3)
	// 3-byte ABX NOPs: $1C, $3C, $5C, $7C, $DC, $FC (4 cycles +1 page cross).
	for _, op := range []byte{0x1C, 0x3C, 0x5C, 0x7C, 0xDC, 0xFC} {
		set(op, "NOP", ABX, 3, 4, true, opNOP3)
	}

	// Unstable opcodes — explicitly left as 1-byte NOPs (the default fill).
	// Listing them here documents the decision:
	//   $93 AHX (zp),Y     $9F AHX abs,Y
	//   $9C SHY abs,X
	//   $9E SHX abs,Y
	//   $9B TAS abs,Y
	//   $BB LAS abs,Y
	//   $8B XAA #imm       $AB LAX #imm  (XAA-family)
	//   $02 $12 $22 $32 $42 $52 $62 $72 $92 $B2 $D2 $F2  KIL/JAM
	// These either depend on bus capacitance or halt the CPU; the default
	// 2-cycle NOP fill is the safest stub.
}
