package dap

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/nkane/chippy/internal/cpu"
)

// handleDisassemble renders InstructionCount instructions starting at
// MemoryReference (+ Offset bytes). Variant-aware via cpu.DisasmCPU so
// CMOS-only mnemonics decode correctly.
//
// NB: a negative InstructionOffset means "show N instructions before the
// reference address." That's a hard problem on the 6502 because of
// variable-width opcodes — proper support would require the same
// walk-back heuristic the TUI uses. Clamped to 0 in this v1; the
// editor sees fewer pre-context instructions than requested but the
// disasm view at PC works.
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
	start := uint16(int(base) + args.Offset)
	count := args.InstructionCount
	if count <= 0 {
		count = 16
	}

	instructions := make([]DisassembledInstruction, 0, count)
	addr := start
	for i := 0; i < count; i++ {
		text, n := cpu.DisasmCPU(s.cpu, addr)
		bytesHex := make([]string, n)
		for j := 0; j < n; j++ {
			bytesHex[j] = fmt.Sprintf("%02X", s.ram.Read(addr+uint16(j)))
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
		instructions = append(instructions, ins)
		next := uint32(addr) + uint32(n)
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

// handleReadMemory returns a byte window starting at MemoryReference (+
// Offset). Reads go directly through cpu.RAM, bypassing MMIO peripherals
// whose Read may have side effects (e.g. clearing keyboard status). This
// matches DAP's expectation that memory inspection is side-effect-free.
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
	start := int(base) + args.Offset
	if start < 0 || start > 0xFFFF {
		s.sendErrorResponse(req, fmt.Sprintf("address out of range: %d", start))
		return
	}
	n := args.Count
	if start+n > 0x10000 {
		n = 0x10000 - start
	}
	raw := make([]byte, n)
	for i := 0; i < n; i++ {
		raw[i] = s.ram.Read(uint16(start + i))
	}

	type body struct {
		Address string `json:"address"`
		Data    string `json:"data"`
	}
	s.sendResponse(req, body{
		Address: fmt.Sprintf("$%04X", start),
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
	start := int(base) + args.Offset
	if start < 0 || start > 0xFFFF {
		s.sendErrorResponse(req, fmt.Sprintf("address out of range: %d", start))
		return
	}
	written := 0
	for i, b := range data {
		a := start + i
		if a > 0xFFFF {
			break
		}
		s.ram.Write(uint16(a), b)
		written++
	}

	type body struct {
		BytesWritten int `json:"bytesWritten"`
		Offset       int `json:"offset,omitempty"`
	}
	s.sendResponse(req, body{BytesWritten: written, Offset: args.Offset})
}
