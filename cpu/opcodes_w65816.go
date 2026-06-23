package cpu

// 65816 opcode table — PHASE 1 SCAFFOLD (#456).
//
// This is the emulation-mode foundation, NOT the full WDC 65C816 core. It
// derives from the 65C02 (CMOS) table for the 6502/65C02-shared instructions,
// which behave identically in the 65816's emulation mode (E=1): registers are
// 8-bit and a low-byte op leaves the accumulator high byte (CPU.B) intact,
// exactly as the existing handlers do. The mode-control opcodes XCE / SEP / REP
// are layered on top so a program can probe + toggle E and the M/X width bits.
//
// NOT yet modeled (the #456 epic — a full second 16-bit core validated against
// the Tom Harte 65816 corpus, 256 opcodes × emulation/native):
//   - The 65816-divergent opcode slots: the $x7/$xF positions that are
//     RMB/SMB/BBR/BBS on the 65C02 are long / [dp] / stack-relative
//     instructions on the 65816, and the remaining "???" CMOS-NOP slots are
//     real 65816 instructions (MVN/MVP, PEA/PEI/PER, the bank-transfer ops…).
//   - Native mode (E=0) 16-bit accumulator/index operations (M/X-gated widths).
//   - New addressing modes: absolute long, [dp], [dp],Y, stack-relative,
//     (sr),Y, [abs] / (abs,X) jumps, block move.
//   - 24-bit (DBR/PBR-relative) effective-address formation + the D register.
//
// Built in init() AFTER opcodes_cmos.go — filename lex order puts
// opcodes_w65816.go last, so OpcodesCMOS is fully populated when this copies it.
var Opcodes65816 [256]Instr

func init() {
	Opcodes65816 = OpcodesCMOS // shared emulation-mode base (value copy)

	set := func(op byte, name string, mode AddrMode, bytes, cycles int, fn func(*CPU, uint16, AddrMode)) {
		Opcodes65816[op] = Instr{Name: name, Mode: mode, Bytes: bytes, Cycles: cycles, Exec: fn}
	}
	set(0xFB, "XCE", IMP, 1, 2, opXCE)
	set(0xC2, "REP", IMM, 2, 3, opREP)
	set(0xE2, "SEP", IMM, 2, 3, opSEP)
}

// opXCE exchanges the carry and emulation (E) flags — the 65816's mode switch.
// Entering emulation (E=1) forces 8-bit registers: the stack high byte returns
// to page $01 and the index high bytes truncate away. M/X are forced set while
// E=1 (enforced by the width helpers, which read E first).
func opXCE(c *CPU, _ uint16, _ AddrMode) {
	oldCarry := c.P&FlagC != 0
	if c.E {
		c.P |= FlagC
	} else {
		c.P &^= FlagC
	}
	c.E = oldCarry
	if c.E {
		c.SPHi = 0x01
		c.XH, c.YH = 0, 0
	}
}

// opREP clears the P-flag bits set in the immediate mask (REset Processor
// status bits). In emulation mode the M/X width bits (P bits 5/4) are locked,
// so the mask can't touch them.
func opREP(c *CPU, addr uint16, _ AddrMode) {
	mask := c.read(addr)
	if c.E {
		mask &^= FlagU | FlagB // M/X locked while E=1
	}
	c.P &^= mask
}

// opSEP sets the P-flag bits in the immediate mask (SEt Processor status bits).
// Same emulation-mode M/X lock as REP.
func opSEP(c *CPU, addr uint16, _ AddrMode) {
	mask := c.read(addr)
	if c.E {
		mask &^= FlagU | FlagB
	}
	c.P |= mask
}
