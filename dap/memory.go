package dap

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nkane/chippy/cpu"
)

// handleDisassemble renders InstructionCount instructions starting
// `InstructionOffset` *instructions* before/after MemoryReference (+
// Offset bytes). Variant-aware via cpu.DisasmCPU so CMOS-only mnemonics
// decode correctly.
//
// Negative InstructionOffset (pre-context) is resolved via cpu.WalkBack —
// 6502 has variable-width opcodes so there's no exact backward decode;
// the heuristic walks back up to 64 bytes trying alignments that decode
// cleanly all the way to the reference. Pre-context list is emitted
// first, then forward instructions, until InstructionCount entries are
// produced.
func (s *Server) handleDisassemble(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args DisassembleArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad disassemble args: %v", err))
		return
	}
	base, err := parseDAPNumber(args.MemoryReference)
	if err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad memoryReference %q: %v", args.MemoryReference, err))
		return
	}
	// Offset is signed; large negatives would wrap the uint16 conversion
	// to an unpredictable PC. Clamp to the 16-bit address space.
	pc := int(base) + args.Offset
	if pc < 0 {
		pc = 0
	}
	if pc > 0xFFFF {
		pc = 0xFFFF
	}
	refPC := uint16(pc)
	count := args.InstructionCount
	if count < 0 {
		s.sendErrorResponse(req, fmt.Sprintf("instructionCount must be >= 0; got %d", count))
		return
	}
	if count == 0 {
		count = 16
	}

	instructions := make([]DisassembledInstruction, 0, count)

	// Pre-context: if instructionOffset is negative, walk back |offset|
	// instructions from refPC and emit them first.
	if args.InstructionOffset < 0 {
		back := cpu.WalkBack(s.cpu, refPC, -args.InstructionOffset)
		for _, a := range back {
			if len(instructions) >= count {
				break
			}
			instructions = append(instructions, s.disasmOne(a))
		}
	}

	// Forward decode from refPC (skip if a positive instructionOffset
	// asks for forward-only context — currently treated as alignment
	// at refPC).
	addr := refPC
	for len(instructions) < count {
		ins := s.disasmOne(addr)
		instructions = append(instructions, ins)
		next := uint32(addr) + uint32(s.instrLen(addr))
		if next > 0xFFFF {
			break
		}
		addr = uint16(next)
	}

	type body struct {
		Instructions []DisassembledInstruction `json:"instructions"`
	}
	s.sendResponse(req, body{Instructions: instructions})
}

// isDataAddr reports whether addr falls in a source-map data range — rendered
// as a `.byte` literal rather than decoded as an instruction (issue #452, to
// match the TUI's data-segment display). nil-safe via SourceMap.IsData.
func (s *Server) isDataAddr(addr uint16) bool {
	return s.srcMap.IsData(addr)
}

// instrLen is the byte stride to the next disassembly line at addr: 1 for a
// data byte, the decoded instruction length otherwise.
func (s *Server) instrLen(addr uint16) int {
	if s.isDataAddr(addr) {
		return 1
	}
	_, n := cpu.DisasmCPU(s.cpu, addr)
	return n
}

// disasmOne formats one DisassembledInstruction at addr with bytes,
// symbol, and source location filled in when available. Data-range addresses
// render as `.byte $XX` (one byte) instead of a decoded instruction.
func (s *Server) disasmOne(addr uint16) DisassembledInstruction {
	var text string
	var n int
	if s.isDataAddr(addr) {
		text, n = fmt.Sprintf(".byte $%02X", s.peekByte(addr)), 1
	} else {
		text, n = cpu.DisasmCPU(s.cpu, addr)
	}
	bytesHex := make([]string, n)
	for j := 0; j < n; j++ {
		bytesHex[j] = fmt.Sprintf("%02X", s.peekByte(addr+uint16(j)))
	}
	ins := DisassembledInstruction{
		Address:          fmt.Sprintf("$%04X", addr),
		InstructionBytes: strings.Join(bytesHex, " "),
		Instruction:      text,
	}
	if s.syms != nil {
		ins.Symbol = s.syms.Lookup(addr)
	}
	if s.srcMap != nil {
		if loc, ok := s.srcMap.PCToSrc[addr]; ok {
			ins.Location = &Source{Name: filepath.Base(loc.File), Path: loc.File}
			ins.Line = loc.Line
		}
	}
	return ins
}

// peekByte is the side-effect-free byte read used by readMemory and
// disassemble. When MMIO is wired, we consult it so peripherals (like
// the NES cart's PRG-ROM at $C000-$FFFF) surface their bytes; the
// MMIO.Peek path uses each peripheral's Peeker interface if it
// implements one and falls back to Inner.Read otherwise — which keeps
// memory inspection side-effect-free even for peripherals (PPU,
// joypad, keyboard) whose Read mutates state.
//
// When MMIO is nil (older test fixtures, headless paths), we read
// ram directly — same behavior as before.
func (s *Server) peekByte(addr uint16) byte {
	if s.mmio != nil {
		return s.mmio.Peek(addr)
	}
	return s.ram.Read(addr)
}

// peekByte24 is the bank-aware side-effect-free read. Bank 0 routes through the
// MMIO-aware peekByte; banks 1-255 read the 65816's flat store (Banked24.Read24
// has no side effects beyond bank 0). When no bank-aware bus is wired (8/16-bit
// variants), any address masks back into bank 0 — the pre-#505 behavior.
func (s *Server) peekByte24(addr uint32) byte {
	if addr < 0x10000 || s.banked == nil {
		return s.peekByte(uint16(addr))
	}
	return s.banked.Read24(addr)
}

// writeByte24 is the bank-aware DAP poke. Bank 0 writes ram directly (bypassing
// MMIO, as writeMemory always has); banks 1-255 write the flat store. Without a
// bank-aware bus, the address masks into bank 0.
func (s *Server) writeByte24(addr uint32, v byte) {
	if addr < 0x10000 || s.banked == nil {
		s.ram.Write(uint16(addr), v)
		return
	}
	s.banked.Write24(addr, v)
}

// memMax is the inclusive top address the debuggee can address: 16 MB for the
// 65816 (bank-aware bus wired), else the 64 KiB 6502 space.
func (s *Server) memMax() int {
	if s.banked != nil {
		return 0xFFFFFF
	}
	return 0xFFFF
}

// fmtAddr renders a DAP address — 6 hex digits for the bank-aware 65816 space,
// 4 for the 64 KiB 6502 space.
func (s *Server) fmtAddr(a int) string {
	if s.banked != nil {
		return fmt.Sprintf("$%06X", a)
	}
	return fmt.Sprintf("$%04X", a)
}

// handleReadMemory returns a byte window starting at MemoryReference (+
// Offset). Reads go through MMIO.Peek (or ram directly when no MMIO is
// wired) so peripherals' state shows up to inspectors without
// triggering Read side effects.
func (s *Server) handleReadMemory(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args ReadMemoryArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad readMemory args: %v", err))
		return
	}
	base, err := parseDAPNumber(args.MemoryReference)
	if err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad memoryReference %q: %v", args.MemoryReference, err))
		return
	}
	top := s.memMax()
	start := int(base) + args.Offset
	if start < 0 || start > top {
		s.sendErrorResponse(req, fmt.Sprintf("address out of range: %d", start))
		return
	}
	if args.Count < 0 {
		s.sendErrorResponse(req, fmt.Sprintf("count must be >= 0; got %d", args.Count))
		return
	}
	n := args.Count
	if start+n > top+1 {
		n = top + 1 - start
	}
	raw := make([]byte, n)
	for i := 0; i < n; i++ {
		raw[i] = s.peekByte24(uint32(start + i))
	}

	type body struct {
		Address string `json:"address"`
		Data    string `json:"data"`
	}
	s.sendResponse(req, body{
		Address: s.fmtAddr(start),
		Data:    base64.StdEncoding.EncodeToString(raw),
	})
}

// handleWriteMemory writes a base64-decoded byte window at MemoryReference
// (+ Offset). Writes bypass MMIO for the same reason readMemory does:
// DAP memory pokes shouldn't accidentally drive a peripheral.
func (s *Server) handleWriteMemory(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args WriteMemoryArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad writeMemory args: %v", err))
		return
	}
	base, err := parseDAPNumber(args.MemoryReference)
	if err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad memoryReference %q: %v", args.MemoryReference, err))
		return
	}
	data, err := base64.StdEncoding.DecodeString(args.Data)
	if err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad base64 data: %v", err))
		return
	}
	top := s.memMax()
	start := int(base) + args.Offset
	if start < 0 || start > top {
		s.sendErrorResponse(req, fmt.Sprintf("address out of range: %d", start))
		return
	}
	end := start + len(data)
	// AllowPartial = false (the spec default): the entire payload must
	// fit within the address space, otherwise reject before writing
	// anything. With AllowPartial = true, write what fits.
	if !args.AllowPartial && end > top+1 {
		s.sendErrorResponse(req, fmt.Sprintf(
			"write of %d bytes at %s would overflow the address space; set allowPartial=true to accept truncation",
			len(data), s.fmtAddr(start)))
		return
	}
	written := 0
	for i, b := range data {
		a := start + i
		if a > top {
			break
		}
		s.writeByte24(uint32(a), b)
		written++
	}

	type body struct {
		BytesWritten int `json:"bytesWritten"`
		Offset       int `json:"offset,omitempty"`
	}
	s.sendResponse(req, body{BytesWritten: written, Offset: args.Offset})
}
