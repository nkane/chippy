package cpu

import "fmt"

// SymLookup returns a name for an address, or "" if none.
type SymLookup func(uint16) string

// Disasm produces a single-instruction disassembly starting at addr.
// Returns formatted text + number of bytes consumed.
func Disasm(bus Bus, addr uint16) (string, int) {
	return DisasmWithSyms(bus, addr, nil)
}

// DisasmWithSyms is like Disasm but substitutes symbol names for absolute
// targets when the lookup returns a non-empty name.
func DisasmWithSyms(bus Bus, addr uint16, sym SymLookup) (string, int) {
	op := bus.Read(addr)
	in := Opcodes[op]
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
	case ABS:
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
	}

	if in.Bytes == 1 {
		return fmt.Sprintf("%-4s %s", in.Name, operand), 1
	}
	return fmt.Sprintf("%-4s %s", in.Name, operand), in.Bytes
}
