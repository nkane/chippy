package cpu

// Instruction metadata.
type Instr struct {
	Name    string
	Mode    AddrMode
	Bytes   int
	Cycles  int
	PageAdd bool // +1 cycle if page crossed
	Exec    func(c *CPU, addr uint16, mode AddrMode)
}

// Opcode table. Index = opcode byte. Unofficial opcodes treated as NOP-like or panic-on-execute? -> NOP-like 2-byte.
var Opcodes [256]Instr

func init() {
	// fill with illegal -> NOP placeholder
	for i := range Opcodes {
		Opcodes[i] = Instr{Name: "???", Mode: IMP, Bytes: 1, Cycles: 2, Exec: opNOP}
	}

	// helper
	set := func(op byte, name string, mode AddrMode, bytes, cycles int, pageAdd bool, fn func(*CPU, uint16, AddrMode)) {
		Opcodes[op] = Instr{name, mode, bytes, cycles, pageAdd, fn}
	}

	// LDA
	set(0xA9, "LDA", IMM, 2, 2, false, opLDA)
	set(0xA5, "LDA", ZP, 2, 3, false, opLDA)
	set(0xB5, "LDA", ZPX, 2, 4, false, opLDA)
	set(0xAD, "LDA", ABS, 3, 4, false, opLDA)
	set(0xBD, "LDA", ABX, 3, 4, true, opLDA)
	set(0xB9, "LDA", ABY, 3, 4, true, opLDA)
	set(0xA1, "LDA", IZX, 2, 6, false, opLDA)
	set(0xB1, "LDA", IZY, 2, 5, true, opLDA)
	// LDX
	set(0xA2, "LDX", IMM, 2, 2, false, opLDX)
	set(0xA6, "LDX", ZP, 2, 3, false, opLDX)
	set(0xB6, "LDX", ZPY, 2, 4, false, opLDX)
	set(0xAE, "LDX", ABS, 3, 4, false, opLDX)
	set(0xBE, "LDX", ABY, 3, 4, true, opLDX)
	// LDY
	set(0xA0, "LDY", IMM, 2, 2, false, opLDY)
	set(0xA4, "LDY", ZP, 2, 3, false, opLDY)
	set(0xB4, "LDY", ZPX, 2, 4, false, opLDY)
	set(0xAC, "LDY", ABS, 3, 4, false, opLDY)
	set(0xBC, "LDY", ABX, 3, 4, true, opLDY)
	// STA
	set(0x85, "STA", ZP, 2, 3, false, opSTA)
	set(0x95, "STA", ZPX, 2, 4, false, opSTA)
	set(0x8D, "STA", ABS, 3, 4, false, opSTA)
	set(0x9D, "STA", ABX, 3, 5, false, opSTA)
	set(0x99, "STA", ABY, 3, 5, false, opSTA)
	set(0x81, "STA", IZX, 2, 6, false, opSTA)
	set(0x91, "STA", IZY, 2, 6, false, opSTA)
	// STX
	set(0x86, "STX", ZP, 2, 3, false, opSTX)
	set(0x96, "STX", ZPY, 2, 4, false, opSTX)
	set(0x8E, "STX", ABS, 3, 4, false, opSTX)
	// STY
	set(0x84, "STY", ZP, 2, 3, false, opSTY)
	set(0x94, "STY", ZPX, 2, 4, false, opSTY)
	set(0x8C, "STY", ABS, 3, 4, false, opSTY)
	// transfers
	set(0xAA, "TAX", IMP, 1, 2, false, opTAX)
	set(0xA8, "TAY", IMP, 1, 2, false, opTAY)
	set(0xBA, "TSX", IMP, 1, 2, false, opTSX)
	set(0x8A, "TXA", IMP, 1, 2, false, opTXA)
	set(0x9A, "TXS", IMP, 1, 2, false, opTXS)
	set(0x98, "TYA", IMP, 1, 2, false, opTYA)
	// stack
	set(0x48, "PHA", IMP, 1, 3, false, opPHA)
	set(0x08, "PHP", IMP, 1, 3, false, opPHP)
	set(0x68, "PLA", IMP, 1, 4, false, opPLA)
	set(0x28, "PLP", IMP, 1, 4, false, opPLP)
	// logical AND
	set(0x29, "AND", IMM, 2, 2, false, opAND)
	set(0x25, "AND", ZP, 2, 3, false, opAND)
	set(0x35, "AND", ZPX, 2, 4, false, opAND)
	set(0x2D, "AND", ABS, 3, 4, false, opAND)
	set(0x3D, "AND", ABX, 3, 4, true, opAND)
	set(0x39, "AND", ABY, 3, 4, true, opAND)
	set(0x21, "AND", IZX, 2, 6, false, opAND)
	set(0x31, "AND", IZY, 2, 5, true, opAND)
	// EOR
	set(0x49, "EOR", IMM, 2, 2, false, opEOR)
	set(0x45, "EOR", ZP, 2, 3, false, opEOR)
	set(0x55, "EOR", ZPX, 2, 4, false, opEOR)
	set(0x4D, "EOR", ABS, 3, 4, false, opEOR)
	set(0x5D, "EOR", ABX, 3, 4, true, opEOR)
	set(0x59, "EOR", ABY, 3, 4, true, opEOR)
	set(0x41, "EOR", IZX, 2, 6, false, opEOR)
	set(0x51, "EOR", IZY, 2, 5, true, opEOR)
	// ORA
	set(0x09, "ORA", IMM, 2, 2, false, opORA)
	set(0x05, "ORA", ZP, 2, 3, false, opORA)
	set(0x15, "ORA", ZPX, 2, 4, false, opORA)
	set(0x0D, "ORA", ABS, 3, 4, false, opORA)
	set(0x1D, "ORA", ABX, 3, 4, true, opORA)
	set(0x19, "ORA", ABY, 3, 4, true, opORA)
	set(0x01, "ORA", IZX, 2, 6, false, opORA)
	set(0x11, "ORA", IZY, 2, 5, true, opORA)
	// BIT
	set(0x24, "BIT", ZP, 2, 3, false, opBIT)
	set(0x2C, "BIT", ABS, 3, 4, false, opBIT)
	// arithmetic
	set(0x69, "ADC", IMM, 2, 2, false, opADC)
	set(0x65, "ADC", ZP, 2, 3, false, opADC)
	set(0x75, "ADC", ZPX, 2, 4, false, opADC)
	set(0x6D, "ADC", ABS, 3, 4, false, opADC)
	set(0x7D, "ADC", ABX, 3, 4, true, opADC)
	set(0x79, "ADC", ABY, 3, 4, true, opADC)
	set(0x61, "ADC", IZX, 2, 6, false, opADC)
	set(0x71, "ADC", IZY, 2, 5, true, opADC)
	set(0xE9, "SBC", IMM, 2, 2, false, opSBC)
	set(0xE5, "SBC", ZP, 2, 3, false, opSBC)
	set(0xF5, "SBC", ZPX, 2, 4, false, opSBC)
	set(0xED, "SBC", ABS, 3, 4, false, opSBC)
	set(0xFD, "SBC", ABX, 3, 4, true, opSBC)
	set(0xF9, "SBC", ABY, 3, 4, true, opSBC)
	set(0xE1, "SBC", IZX, 2, 6, false, opSBC)
	set(0xF1, "SBC", IZY, 2, 5, true, opSBC)
	// CMP
	set(0xC9, "CMP", IMM, 2, 2, false, opCMP)
	set(0xC5, "CMP", ZP, 2, 3, false, opCMP)
	set(0xD5, "CMP", ZPX, 2, 4, false, opCMP)
	set(0xCD, "CMP", ABS, 3, 4, false, opCMP)
	set(0xDD, "CMP", ABX, 3, 4, true, opCMP)
	set(0xD9, "CMP", ABY, 3, 4, true, opCMP)
	set(0xC1, "CMP", IZX, 2, 6, false, opCMP)
	set(0xD1, "CMP", IZY, 2, 5, true, opCMP)
	// CPX / CPY
	set(0xE0, "CPX", IMM, 2, 2, false, opCPX)
	set(0xE4, "CPX", ZP, 2, 3, false, opCPX)
	set(0xEC, "CPX", ABS, 3, 4, false, opCPX)
	set(0xC0, "CPY", IMM, 2, 2, false, opCPY)
	set(0xC4, "CPY", ZP, 2, 3, false, opCPY)
	set(0xCC, "CPY", ABS, 3, 4, false, opCPY)
	// INC / DEC
	set(0xE6, "INC", ZP, 2, 5, false, opINC)
	set(0xF6, "INC", ZPX, 2, 6, false, opINC)
	set(0xEE, "INC", ABS, 3, 6, false, opINC)
	set(0xFE, "INC", ABX, 3, 7, false, opINC)
	set(0xC6, "DEC", ZP, 2, 5, false, opDEC)
	set(0xD6, "DEC", ZPX, 2, 6, false, opDEC)
	set(0xCE, "DEC", ABS, 3, 6, false, opDEC)
	set(0xDE, "DEC", ABX, 3, 7, false, opDEC)
	set(0xE8, "INX", IMP, 1, 2, false, opINX)
	set(0xC8, "INY", IMP, 1, 2, false, opINY)
	set(0xCA, "DEX", IMP, 1, 2, false, opDEX)
	set(0x88, "DEY", IMP, 1, 2, false, opDEY)
	// shifts (accumulator + memory)
	set(0x0A, "ASL", ACC, 1, 2, false, opASL)
	set(0x06, "ASL", ZP, 2, 5, false, opASL)
	set(0x16, "ASL", ZPX, 2, 6, false, opASL)
	set(0x0E, "ASL", ABS, 3, 6, false, opASL)
	set(0x1E, "ASL", ABX, 3, 7, false, opASL)
	set(0x4A, "LSR", ACC, 1, 2, false, opLSR)
	set(0x46, "LSR", ZP, 2, 5, false, opLSR)
	set(0x56, "LSR", ZPX, 2, 6, false, opLSR)
	set(0x4E, "LSR", ABS, 3, 6, false, opLSR)
	set(0x5E, "LSR", ABX, 3, 7, false, opLSR)
	set(0x2A, "ROL", ACC, 1, 2, false, opROL)
	set(0x26, "ROL", ZP, 2, 5, false, opROL)
	set(0x36, "ROL", ZPX, 2, 6, false, opROL)
	set(0x2E, "ROL", ABS, 3, 6, false, opROL)
	set(0x3E, "ROL", ABX, 3, 7, false, opROL)
	set(0x6A, "ROR", ACC, 1, 2, false, opROR)
	set(0x66, "ROR", ZP, 2, 5, false, opROR)
	set(0x76, "ROR", ZPX, 2, 6, false, opROR)
	set(0x6E, "ROR", ABS, 3, 6, false, opROR)
	set(0x7E, "ROR", ABX, 3, 7, false, opROR)
	// jumps
	set(0x4C, "JMP", ABS, 3, 3, false, opJMP)
	set(0x6C, "JMP", IND, 3, 5, false, opJMP)
	set(0x20, "JSR", JSRABS, 3, 6, false, opJSR)
	set(0x60, "RTS", IMP, 1, 6, false, opRTS)
	set(0x40, "RTI", IMP, 1, 6, false, opRTI)
	// branches
	set(0x10, "BPL", REL, 2, 2, false, opBPL)
	set(0x30, "BMI", REL, 2, 2, false, opBMI)
	set(0x50, "BVC", REL, 2, 2, false, opBVC)
	set(0x70, "BVS", REL, 2, 2, false, opBVS)
	set(0x90, "BCC", REL, 2, 2, false, opBCC)
	set(0xB0, "BCS", REL, 2, 2, false, opBCS)
	set(0xD0, "BNE", REL, 2, 2, false, opBNE)
	set(0xF0, "BEQ", REL, 2, 2, false, opBEQ)
	// flags
	set(0x18, "CLC", IMP, 1, 2, false, opCLC)
	set(0x38, "SEC", IMP, 1, 2, false, opSEC)
	set(0x58, "CLI", IMP, 1, 2, false, opCLI)
	set(0x78, "SEI", IMP, 1, 2, false, opSEI)
	set(0xB8, "CLV", IMP, 1, 2, false, opCLV)
	set(0xD8, "CLD", IMP, 1, 2, false, opCLD)
	set(0xF8, "SED", IMP, 1, 2, false, opSED)
	// system
	set(0x00, "BRK", IMP, 1, 7, false, opBRK)
	set(0xEA, "NOP", IMP, 1, 2, false, opNOP)
}
