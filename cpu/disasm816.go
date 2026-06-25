package cpu

import "fmt"

// 65816 disassembler (#456). The 65816 needs its own decode: it has new
// addressing modes (long, [dp], stack-relative, block move) and, crucially,
// width-dependent immediates — LDA #imm is two operand bytes when the
// accumulator is 16-bit (M=0 in native), one when 8-bit; the index immediates
// follow the X width. Disasm816 reads the live M/X/E state off the CPU so the
// rendered length matches what step816 will actually consume.

type am816 int

const (
	a816Imp   am816 = iota // implied
	a816Acc                // accumulator ("A")
	a816ImmM               // immediate, accumulator width
	a816ImmX               // immediate, index width
	a816Imm8               // immediate, always one byte (REP/SEP/COP/WDM/BRK)
	a816Dp                 // direct page
	a816DpX                // dp,X
	a816DpY                // dp,Y
	a816Idp                // (dp)
	a816IdpX               // (dp,X)
	a816IdpY               // (dp),Y
	a816ILdp               // [dp]
	a816ILdpY              // [dp],Y
	a816Sr                 // sr,S
	a816SrY                // (sr,S),Y
	a816Abs                // abs
	a816AbsX               // abs,X
	a816AbsY               // abs,Y
	a816Long               // long (24-bit)
	a816LongX              // long,X
	a816IAbs               // (abs)
	a816IAbsX              // (abs,X)
	a816ILAbs              // [abs]
	a816Rel                // relative (8-bit branch)
	a816RelL               // relative long (16-bit, BRL/PER)
	a816Block              // block move (MVN/MVP): src,dst banks
)

type insn816 struct {
	name string
	mode am816
}

// operandBytes returns how many bytes the mode's operand occupies given the
// current accumulator/index widths.
func (m am816) operandBytes(mWide, xWide bool) int {
	switch m {
	case a816Imp, a816Acc:
		return 0
	case a816ImmM:
		if mWide {
			return 2
		}
		return 1
	case a816ImmX:
		if xWide {
			return 2
		}
		return 1
	case a816Imm8, a816Dp, a816DpX, a816DpY, a816Idp, a816IdpX, a816IdpY,
		a816ILdp, a816ILdpY, a816Sr, a816SrY, a816Rel:
		return 1
	case a816Abs, a816AbsX, a816AbsY, a816IAbs, a816IAbsX, a816ILAbs, a816RelL, a816Block:
		return 2
	case a816Long, a816LongX:
		return 3
	}
	return 0
}

var table816 = [256]insn816{
	0x00: {"BRK", a816Imm8}, 0x01: {"ORA", a816IdpX}, 0x02: {"COP", a816Imm8}, 0x03: {"ORA", a816Sr},
	0x04: {"TSB", a816Dp}, 0x05: {"ORA", a816Dp}, 0x06: {"ASL", a816Dp}, 0x07: {"ORA", a816ILdp},
	0x08: {"PHP", a816Imp}, 0x09: {"ORA", a816ImmM}, 0x0A: {"ASL", a816Acc}, 0x0B: {"PHD", a816Imp},
	0x0C: {"TSB", a816Abs}, 0x0D: {"ORA", a816Abs}, 0x0E: {"ASL", a816Abs}, 0x0F: {"ORA", a816Long},
	0x10: {"BPL", a816Rel}, 0x11: {"ORA", a816IdpY}, 0x12: {"ORA", a816Idp}, 0x13: {"ORA", a816SrY},
	0x14: {"TRB", a816Dp}, 0x15: {"ORA", a816DpX}, 0x16: {"ASL", a816DpX}, 0x17: {"ORA", a816ILdpY},
	0x18: {"CLC", a816Imp}, 0x19: {"ORA", a816AbsY}, 0x1A: {"INC", a816Acc}, 0x1B: {"TCS", a816Imp},
	0x1C: {"TRB", a816Abs}, 0x1D: {"ORA", a816AbsX}, 0x1E: {"ASL", a816AbsX}, 0x1F: {"ORA", a816LongX},
	0x20: {"JSR", a816Abs}, 0x21: {"AND", a816IdpX}, 0x22: {"JSL", a816Long}, 0x23: {"AND", a816Sr},
	0x24: {"BIT", a816Dp}, 0x25: {"AND", a816Dp}, 0x26: {"ROL", a816Dp}, 0x27: {"AND", a816ILdp},
	0x28: {"PLP", a816Imp}, 0x29: {"AND", a816ImmM}, 0x2A: {"ROL", a816Acc}, 0x2B: {"PLD", a816Imp},
	0x2C: {"BIT", a816Abs}, 0x2D: {"AND", a816Abs}, 0x2E: {"ROL", a816Abs}, 0x2F: {"AND", a816Long},
	0x30: {"BMI", a816Rel}, 0x31: {"AND", a816IdpY}, 0x32: {"AND", a816Idp}, 0x33: {"AND", a816SrY},
	0x34: {"BIT", a816DpX}, 0x35: {"AND", a816DpX}, 0x36: {"ROL", a816DpX}, 0x37: {"AND", a816ILdpY},
	0x38: {"SEC", a816Imp}, 0x39: {"AND", a816AbsY}, 0x3A: {"DEC", a816Acc}, 0x3B: {"TSC", a816Imp},
	0x3C: {"BIT", a816AbsX}, 0x3D: {"AND", a816AbsX}, 0x3E: {"ROL", a816AbsX}, 0x3F: {"AND", a816LongX},
	0x40: {"RTI", a816Imp}, 0x41: {"EOR", a816IdpX}, 0x42: {"WDM", a816Imm8}, 0x43: {"EOR", a816Sr},
	0x44: {"MVP", a816Block}, 0x45: {"EOR", a816Dp}, 0x46: {"LSR", a816Dp}, 0x47: {"EOR", a816ILdp},
	0x48: {"PHA", a816Imp}, 0x49: {"EOR", a816ImmM}, 0x4A: {"LSR", a816Acc}, 0x4B: {"PHK", a816Imp},
	0x4C: {"JMP", a816Abs}, 0x4D: {"EOR", a816Abs}, 0x4E: {"LSR", a816Abs}, 0x4F: {"EOR", a816Long},
	0x50: {"BVC", a816Rel}, 0x51: {"EOR", a816IdpY}, 0x52: {"EOR", a816Idp}, 0x53: {"EOR", a816SrY},
	0x54: {"MVN", a816Block}, 0x55: {"EOR", a816DpX}, 0x56: {"LSR", a816DpX}, 0x57: {"EOR", a816ILdpY},
	0x58: {"CLI", a816Imp}, 0x59: {"EOR", a816AbsY}, 0x5A: {"PHY", a816Imp}, 0x5B: {"TCD", a816Imp},
	0x5C: {"JML", a816Long}, 0x5D: {"EOR", a816AbsX}, 0x5E: {"LSR", a816AbsX}, 0x5F: {"EOR", a816LongX},
	0x60: {"RTS", a816Imp}, 0x61: {"ADC", a816IdpX}, 0x62: {"PER", a816RelL}, 0x63: {"ADC", a816Sr},
	0x64: {"STZ", a816Dp}, 0x65: {"ADC", a816Dp}, 0x66: {"ROR", a816Dp}, 0x67: {"ADC", a816ILdp},
	0x68: {"PLA", a816Imp}, 0x69: {"ADC", a816ImmM}, 0x6A: {"ROR", a816Acc}, 0x6B: {"RTL", a816Imp},
	0x6C: {"JMP", a816IAbs}, 0x6D: {"ADC", a816Abs}, 0x6E: {"ROR", a816Abs}, 0x6F: {"ADC", a816Long},
	0x70: {"BVS", a816Rel}, 0x71: {"ADC", a816IdpY}, 0x72: {"ADC", a816Idp}, 0x73: {"ADC", a816SrY},
	0x74: {"STZ", a816DpX}, 0x75: {"ADC", a816DpX}, 0x76: {"ROR", a816DpX}, 0x77: {"ADC", a816ILdpY},
	0x78: {"SEI", a816Imp}, 0x79: {"ADC", a816AbsY}, 0x7A: {"PLY", a816Imp}, 0x7B: {"TDC", a816Imp},
	0x7C: {"JMP", a816IAbsX}, 0x7D: {"ADC", a816AbsX}, 0x7E: {"ROR", a816AbsX}, 0x7F: {"ADC", a816LongX},
	0x80: {"BRA", a816Rel}, 0x81: {"STA", a816IdpX}, 0x82: {"BRL", a816RelL}, 0x83: {"STA", a816Sr},
	0x84: {"STY", a816Dp}, 0x85: {"STA", a816Dp}, 0x86: {"STX", a816Dp}, 0x87: {"STA", a816ILdp},
	0x88: {"DEY", a816Imp}, 0x89: {"BIT", a816ImmM}, 0x8A: {"TXA", a816Imp}, 0x8B: {"PHB", a816Imp},
	0x8C: {"STY", a816Abs}, 0x8D: {"STA", a816Abs}, 0x8E: {"STX", a816Abs}, 0x8F: {"STA", a816Long},
	0x90: {"BCC", a816Rel}, 0x91: {"STA", a816IdpY}, 0x92: {"STA", a816Idp}, 0x93: {"STA", a816SrY},
	0x94: {"STY", a816DpX}, 0x95: {"STA", a816DpX}, 0x96: {"STX", a816DpY}, 0x97: {"STA", a816ILdpY},
	0x98: {"TYA", a816Imp}, 0x99: {"STA", a816AbsY}, 0x9A: {"TXS", a816Imp}, 0x9B: {"TXY", a816Imp},
	0x9C: {"STZ", a816Abs}, 0x9D: {"STA", a816AbsX}, 0x9E: {"STZ", a816AbsX}, 0x9F: {"STA", a816LongX},
	0xA0: {"LDY", a816ImmX}, 0xA1: {"LDA", a816IdpX}, 0xA2: {"LDX", a816ImmX}, 0xA3: {"LDA", a816Sr},
	0xA4: {"LDY", a816Dp}, 0xA5: {"LDA", a816Dp}, 0xA6: {"LDX", a816Dp}, 0xA7: {"LDA", a816ILdp},
	0xA8: {"TAY", a816Imp}, 0xA9: {"LDA", a816ImmM}, 0xAA: {"TAX", a816Imp}, 0xAB: {"PLB", a816Imp},
	0xAC: {"LDY", a816Abs}, 0xAD: {"LDA", a816Abs}, 0xAE: {"LDX", a816Abs}, 0xAF: {"LDA", a816Long},
	0xB0: {"BCS", a816Rel}, 0xB1: {"LDA", a816IdpY}, 0xB2: {"LDA", a816Idp}, 0xB3: {"LDA", a816SrY},
	0xB4: {"LDY", a816DpX}, 0xB5: {"LDA", a816DpX}, 0xB6: {"LDX", a816DpY}, 0xB7: {"LDA", a816ILdpY},
	0xB8: {"CLV", a816Imp}, 0xB9: {"LDA", a816AbsY}, 0xBA: {"TSX", a816Imp}, 0xBB: {"TYX", a816Imp},
	0xBC: {"LDY", a816AbsX}, 0xBD: {"LDA", a816AbsX}, 0xBE: {"LDX", a816AbsY}, 0xBF: {"LDA", a816LongX},
	0xC0: {"CPY", a816ImmX}, 0xC1: {"CMP", a816IdpX}, 0xC2: {"REP", a816Imm8}, 0xC3: {"CMP", a816Sr},
	0xC4: {"CPY", a816Dp}, 0xC5: {"CMP", a816Dp}, 0xC6: {"DEC", a816Dp}, 0xC7: {"CMP", a816ILdp},
	0xC8: {"INY", a816Imp}, 0xC9: {"CMP", a816ImmM}, 0xCA: {"DEX", a816Imp}, 0xCB: {"WAI", a816Imp},
	0xCC: {"CPY", a816Abs}, 0xCD: {"CMP", a816Abs}, 0xCE: {"DEC", a816Abs}, 0xCF: {"CMP", a816Long},
	0xD0: {"BNE", a816Rel}, 0xD1: {"CMP", a816IdpY}, 0xD2: {"CMP", a816Idp}, 0xD3: {"CMP", a816SrY},
	0xD4: {"PEI", a816Idp}, 0xD5: {"CMP", a816DpX}, 0xD6: {"DEC", a816DpX}, 0xD7: {"CMP", a816ILdpY},
	0xD8: {"CLD", a816Imp}, 0xD9: {"CMP", a816AbsY}, 0xDA: {"PHX", a816Imp}, 0xDB: {"STP", a816Imp},
	0xDC: {"JML", a816ILAbs}, 0xDD: {"CMP", a816AbsX}, 0xDE: {"DEC", a816AbsX}, 0xDF: {"CMP", a816LongX},
	0xE0: {"CPX", a816ImmX}, 0xE1: {"SBC", a816IdpX}, 0xE2: {"SEP", a816Imm8}, 0xE3: {"SBC", a816Sr},
	0xE4: {"CPX", a816Dp}, 0xE5: {"SBC", a816Dp}, 0xE6: {"INC", a816Dp}, 0xE7: {"SBC", a816ILdp},
	0xE8: {"INX", a816Imp}, 0xE9: {"SBC", a816ImmM}, 0xEA: {"NOP", a816Imp}, 0xEB: {"XBA", a816Imp},
	0xEC: {"CPX", a816Abs}, 0xED: {"SBC", a816Abs}, 0xEE: {"INC", a816Abs}, 0xEF: {"SBC", a816Long},
	0xF0: {"BEQ", a816Rel}, 0xF1: {"SBC", a816IdpY}, 0xF2: {"SBC", a816Idp}, 0xF3: {"SBC", a816SrY},
	0xF4: {"PEA", a816Abs}, 0xF5: {"SBC", a816DpX}, 0xF6: {"INC", a816DpX}, 0xF7: {"SBC", a816ILdpY},
	0xF8: {"SED", a816Imp}, 0xF9: {"SBC", a816AbsY}, 0xFA: {"PLX", a816Imp}, 0xFB: {"XCE", a816Imp},
	0xFC: {"JSR", a816IAbsX}, 0xFD: {"SBC", a816AbsX}, 0xFE: {"INC", a816AbsX}, 0xFF: {"SBC", a816LongX},
}

// Disasm816 disassembles one 65816 instruction at addr (bank 0 of bus), using
// the CPU's current M/X/E width state to size immediates. Returns the text and
// total instruction length in bytes.
func Disasm816(c *CPU, bus Bus, addr uint16) (string, int) {
	op := bus.Read(addr)
	in := table816[op]
	b1 := bus.Read(addr + 1)
	b2 := bus.Read(addr + 2)
	b3 := bus.Read(addr + 3)
	w16 := uint16(b2)<<8 | uint16(b1)
	long := uint32(b3)<<16 | uint32(b2)<<8 | uint32(b1)

	var operand string
	switch in.mode {
	case a816Imp:
	case a816Acc:
		operand = "A"
	case a816ImmM:
		if c.mWide() {
			operand = fmt.Sprintf("#$%04X", w16)
		} else {
			operand = fmt.Sprintf("#$%02X", b1)
		}
	case a816ImmX:
		if c.xWide() {
			operand = fmt.Sprintf("#$%04X", w16)
		} else {
			operand = fmt.Sprintf("#$%02X", b1)
		}
	case a816Imm8:
		operand = fmt.Sprintf("#$%02X", b1)
	case a816Dp:
		operand = fmt.Sprintf("$%02X", b1)
	case a816DpX:
		operand = fmt.Sprintf("$%02X,X", b1)
	case a816DpY:
		operand = fmt.Sprintf("$%02X,Y", b1)
	case a816Idp:
		operand = fmt.Sprintf("($%02X)", b1)
	case a816IdpX:
		operand = fmt.Sprintf("($%02X,X)", b1)
	case a816IdpY:
		operand = fmt.Sprintf("($%02X),Y", b1)
	case a816ILdp:
		operand = fmt.Sprintf("[$%02X]", b1)
	case a816ILdpY:
		operand = fmt.Sprintf("[$%02X],Y", b1)
	case a816Sr:
		operand = fmt.Sprintf("$%02X,S", b1)
	case a816SrY:
		operand = fmt.Sprintf("($%02X,S),Y", b1)
	case a816Abs:
		operand = fmt.Sprintf("$%04X", w16)
	case a816AbsX:
		operand = fmt.Sprintf("$%04X,X", w16)
	case a816AbsY:
		operand = fmt.Sprintf("$%04X,Y", w16)
	case a816Long:
		operand = fmt.Sprintf("$%06X", long)
	case a816LongX:
		operand = fmt.Sprintf("$%06X,X", long)
	case a816IAbs:
		operand = fmt.Sprintf("($%04X)", w16)
	case a816IAbsX:
		operand = fmt.Sprintf("($%04X,X)", w16)
	case a816ILAbs:
		operand = fmt.Sprintf("[$%04X]", w16)
	case a816Rel:
		target := uint16(int32(addr+2) + int32(int8(b1)))
		operand = fmt.Sprintf("$%04X", target)
	case a816RelL:
		target := uint16(int32(addr+3) + int32(int16(w16)))
		operand = fmt.Sprintf("$%04X", target)
	case a816Block:
		// MVN/MVP src,dst — operand bytes are dest then source; assembler
		// syntax lists source first.
		operand = fmt.Sprintf("$%02X,$%02X", b2, b1)
	}

	size := 1 + in.mode.operandBytes(c.mWide(), c.xWide())
	if operand == "" {
		return in.name, size
	}
	return fmt.Sprintf("%-4s %s", in.name, operand), size
}
