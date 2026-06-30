package cpu

import "fmt"

// SymLookup returns a name for an address, or "" if none.
type SymLookup func(uint16) string

// Disasm produces a single-instruction NMOS disassembly starting at addr.
// Returns formatted text + number of bytes consumed.
//
// CMOS-aware callers should use DisasmCPU / DisasmCPUWithSyms instead so
// CMOS-only mnemonics (STZ, PHX, BRA, BBR0-7, etc.) render correctly. This
// function is kept as a thin NMOS-default wrapper for tests and callers
// without a CPU handy.
func Disasm(bus Bus, addr uint16) (string, int) {
	return disasmWithTable(bus, addr, &Opcodes, nil)
}

// DisasmWithSyms is like Disasm but substitutes symbol names for absolute
// targets when the lookup returns a non-empty name.
func DisasmWithSyms(bus Bus, addr uint16, sym SymLookup) (string, int) {
	return disasmWithTable(bus, addr, &Opcodes, sym)
}

// DisasmCPU is the variant-aware disassembler. It uses the opcode table the
// CPU is actually executing, so a 65C02 program shows CMOS mnemonics
// (STZ, PHX, BRA, BBR0-7, …) instead of whatever the NMOS slot happens
// to be (often an illegal NOP).
func DisasmCPU(c *CPU, addr uint16) (string, int) {
	return DisasmCPUAt(c, c.Bus, addr)
}

// DisasmCPUAt is DisasmCPU reading instruction bytes through the supplied bus
// instead of c.Bus. It lets a caller disassemble a 65816 bank other than the
// one c.Bus exposes by passing a bank view (#505 cross-bank disassembly); addr
// is the 16-bit offset within that bank.
func DisasmCPUAt(c *CPU, bus Bus, addr uint16) (string, int) {
	if c.Variant == VariantW65816 {
		return Disasm816(c, bus, addr)
	}
	return disasmWithTable(bus, addr, c.opcodes, nil)
}

// DisasmCPUWithSyms is the symbol-aware variant of DisasmCPU.
func DisasmCPUWithSyms(c *CPU, addr uint16, sym SymLookup) (string, int) {
	if c.Variant == VariantW65816 {
		return Disasm816(c, c.Bus, addr)
	}
	return disasmWithTable(c.Bus, addr, c.opcodes, sym)
}

// WalkBack returns up to n instruction-start addresses immediately
// preceding pc, in ascending order. 6502 has variable-width opcodes so
// there's no exact backward decode; we try every starting offset
// 1..maxLook back and pick the alignment that decodes cleanly all the
// way to pc with the most instructions.
//
// Shared between the TUI's disassembly panel scroller and the DAP
// server's `disassemble` handler when the editor requests pre-context
// (negative instructionOffset).
func WalkBack(c *CPU, pc uint16, n int) []uint16 {
	return WalkBackAt(c, c.Bus, pc, n)
}

// WalkBackAt is WalkBack reading through the supplied bus — the bus-explicit
// sibling DisasmCPUAt is to DisasmCPU, used for 65816 cross-bank pre-context
// (#505).
func WalkBackAt(c *CPU, bus Bus, pc uint16, n int) []uint16 {
	if n <= 0 || pc == 0 {
		return nil
	}
	const maxLook = 64 // bytes of lookback
	bestSeq := []uint16{}
	for back := 1; back <= maxLook; back++ {
		if int(pc)-back < 0 {
			break
		}
		start := pc - uint16(back)
		var seq []uint16
		cur := start
		ok := true
		for cur < pc {
			seq = append(seq, cur)
			_, sz := DisasmCPUAt(c, bus, cur)
			next := uint32(cur) + uint32(sz)
			if next > uint32(pc) {
				ok = false
				break
			}
			cur = uint16(next)
		}
		if !ok || cur != pc {
			continue
		}
		// Prefer sequences with more instructions; at equal length prefer
		// the earlier start (larger back distance), which biases toward
		// real code boundaries when illegal-opcode slots happen to
		// decode in the middle of a real instruction.
		if len(seq) > len(bestSeq) || (len(seq) == len(bestSeq) && len(seq) > 0 && (len(bestSeq) == 0 || seq[0] < bestSeq[0])) {
			bestSeq = seq
		}
	}
	if len(bestSeq) > n {
		bestSeq = bestSeq[len(bestSeq)-n:]
	}
	return bestSeq
}

// disasmWithTable is the shared core. Both the legacy NMOS-fixed API and
// the CPU-variant-aware API funnel through here so there's exactly one
// formatter to maintain.
func disasmWithTable(bus Bus, addr uint16, table *[256]Instr, sym SymLookup) (string, int) {
	op := bus.Read(addr)
	in := table[op]
	b1 := bus.Read(addr + 1)
	b2 := bus.Read(addr + 2)

	name := func(a uint16, fallback string) string {
		if sym != nil {
			if n := sym(a); n != "" {
				return n
			}
		}
		return fallback
	}

	var operand string
	switch in.Mode {
	case IMP:
		operand = ""
	case ACC:
		operand = "A"
	case IMM:
		operand = fmt.Sprintf("#$%02X", b1)
	case ZP:
		operand = name(uint16(b1), fmt.Sprintf("$%02X", b1))
	case ZPX:
		operand = fmt.Sprintf("%s,X", name(uint16(b1), fmt.Sprintf("$%02X", b1)))
	case ZPY:
		operand = fmt.Sprintf("%s,Y", name(uint16(b1), fmt.Sprintf("$%02X", b1)))
	case REL:
		target := uint16(int32(addr+2) + int32(int8(b1)))
		operand = name(target, fmt.Sprintf("$%04X", target))
	case ABS, JSRABS:
		t := uint16(b2)<<8 | uint16(b1)
		operand = name(t, fmt.Sprintf("$%04X", t))
	case ABX:
		t := uint16(b2)<<8 | uint16(b1)
		operand = fmt.Sprintf("%s,X", name(t, fmt.Sprintf("$%04X", t)))
	case ABY:
		t := uint16(b2)<<8 | uint16(b1)
		operand = fmt.Sprintf("%s,Y", name(t, fmt.Sprintf("$%04X", t)))
	case IND:
		t := uint16(b2)<<8 | uint16(b1)
		operand = fmt.Sprintf("(%s)", name(t, fmt.Sprintf("$%04X", t)))
	case IZX:
		operand = fmt.Sprintf("(%s,X)", name(uint16(b1), fmt.Sprintf("$%02X", b1)))
	case IZY:
		operand = fmt.Sprintf("(%s),Y", name(uint16(b1), fmt.Sprintf("$%02X", b1)))
	case IZP:
		operand = fmt.Sprintf("(%s)", name(uint16(b1), fmt.Sprintf("$%02X", b1)))
	case IAX:
		t := uint16(b2)<<8 | uint16(b1)
		operand = fmt.Sprintf("(%s,X)", name(t, fmt.Sprintf("$%04X", t)))
	case ZPR:
		target := uint16(int32(addr+3) + int32(int8(b2)))
		operand = fmt.Sprintf("$%02X,%s", b1, name(target, fmt.Sprintf("$%04X", target)))
	}

	if in.Bytes == 1 {
		return fmt.Sprintf("%-4s %s", in.Name, operand), 1
	}
	return fmt.Sprintf("%-4s %s", in.Name, operand), in.Bytes
}
