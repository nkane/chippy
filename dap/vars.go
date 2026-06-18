package dap

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nkane/chippy/cpu"
)

// Variable-reference IDs. Static / well-known because the 6502 has a
// single fixed set of registers and flags; no per-frame variation. Real
// debug targets typically allocate these dynamically, but for 6502 a
// constant map is plain and lets `setVariable` switch on the ref ID
// instead of carrying a generation counter.
const (
	refRegisters = 1
	refFlags     = 2
	refGlobals   = 3
	// refDynamicBase is the first dynamically-allocated variablesReference
	// (array-child handles). Kept clear of the static refs above.
	refDynamicBase = 1000
	// maxGlobals / maxArrayChildren bound the Globals scope so a huge symbol
	// table or a bogus `size=` can't flood the Variables pane.
	maxGlobals       = 1024
	maxArrayChildren = 4096
)

// arrayRef records what a dynamic variablesReference expands to: Count
// consecutive bytes starting at Addr, rendered as name[0..Count-1].
type arrayRef struct {
	Addr  uint16
	Count int
}

func (s *Server) handleStackTrace(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	type body struct {
		StackFrames []StackFrame `json:"stackFrames"`
		TotalFrames int          `json:"totalFrames"`
	}
	frames := make([]StackFrame, 0, 8)
	frames = append(frames, s.frameForPC(0, s.cpu.PC))

	// Walk upward from SP+1; each detected JSR pair becomes another frame.
	// Cap at 64 frames to bound a runaway stack.
	const frameCap = 64
	a := uint16(s.cpu.SP) + 1
	for a <= 0xFF && len(frames) < frameCap {
		sp := uint16(0x0100) | a
		retAddr, target, ok := cpu.DetectStackFrame(s.ram, sp)
		if !ok {
			a++
			continue
		}
		// retAddr is the address RTS will jump to. The caller's PC at the
		// JSR site is retAddr - 1 (the operand byte of the JSR). For the
		// stack-trace `Name` we still report retAddr so the user sees
		// where the routine will resume; ChippyStackAddr / ChippyCallee
		// carry the stack-page slot and the called routine's symbol for the
		// chippy TUI's stack-page panel (issue #449).
		f := s.frameForPC(len(frames), retAddr)
		f.ChippyStackAddr = fmt.Sprintf("$%04X", sp)
		if s.syms != nil {
			f.ChippyCallee = s.syms.Lookup(target)
		}
		frames = append(frames, f)
		a += 2
	}
	s.sendResponse(req, body{StackFrames: frames, TotalFrames: len(frames)})
}

// frameForPC builds a StackFrame at the given ID for the given PC. Source
// info is filled from the loaded .dbg when available; otherwise the
// caller sees a bare `$XXXX` name.
func (s *Server) frameForPC(id int, pc uint16) StackFrame {
	f := StackFrame{
		ID:                          id,
		Name:                        s.nameForPC(pc),
		InstructionPointerReference: fmt.Sprintf("$%04X", pc),
	}
	if s.srcMap != nil {
		if loc, ok := s.srcMap.PCToSrc[pc]; ok {
			name := filepath.Base(loc.File)
			f.Source = &Source{Name: name, Path: loc.File}
			f.Line = loc.Line
			f.Column = 1
		}
	}
	return f
}

func (s *Server) nameForPC(pc uint16) string {
	if s.syms != nil {
		if n := s.syms.Lookup(pc); n != "" {
			return n
		}
	}
	return fmt.Sprintf("$%04X", pc)
}

func (s *Server) handleScopes(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee")
		return
	}
	type body struct {
		Scopes []Scope `json:"scopes"`
	}
	scopes := []Scope{
		{Name: "Registers", VariablesReference: refRegisters, Expensive: false},
		{Name: "Flags", VariablesReference: refFlags, Expensive: false},
	}
	// Globals scope (issue #410): only when symbols are loaded, else the
	// pane would show an empty heading for ROMs launched without a .dbg.
	if s.syms.Has() {
		scopes = append(scopes, Scope{Name: "Globals", VariablesReference: refGlobals, Expensive: false})
	}
	s.sendResponse(req, body{Scopes: scopes})
}

func (s *Server) handleVariables(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee")
		return
	}
	var args VariablesArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad variables args: %v", err))
		return
	}
	type body struct {
		Variables []Variable `json:"variables"`
	}
	switch args.VariablesReference {
	case refRegisters:
		s.sendResponse(req, body{Variables: s.regsVariables()})
	case refFlags:
		s.sendResponse(req, body{Variables: s.flagsVariables()})
	case refGlobals:
		s.sendResponse(req, body{Variables: s.globalsVariables()})
	default:
		if ar, ok := s.varRefs[args.VariablesReference]; ok {
			s.sendResponse(req, body{Variables: s.arrayChildren(ar, args.Start, args.Count)})
			return
		}
		s.sendErrorResponse(req, fmt.Sprintf("unknown variablesReference: %d", args.VariablesReference))
	}
}

// globalsVariables enumerates data symbols for the Globals scope (issue #410).
// A symbol becomes an expandable array when cc65 recorded a `size=` > 1;
// otherwise it's a scalar byte. Code labels (addresses that map to a source
// line) are filtered out — they're instructions, not data. Rebuilds the
// dynamic variablesReference table on each call.
func (s *Server) globalsVariables() []Variable {
	s.varRefs = map[int]arrayRef{}
	s.varRefSeq = refDynamicBase
	out := make([]Variable, 0, 16)
	for _, sym := range s.syms.Symbols() {
		if len(out) >= maxGlobals {
			break
		}
		// Keep data: anything cc65 sized, or anything in a flagged data
		// range; drop pure code labels.
		sized := sym.Size > 0
		if !sized && !s.srcMap.IsData(sym.Addr) {
			continue
		}
		if s.isCodeAddr(sym.Addr) {
			continue
		}
		if sym.Size > 1 {
			count := sym.Size
			if count > maxArrayChildren {
				count = maxArrayChildren
			}
			ref := s.allocArrayRef(sym.Addr, count)
			out = append(out, Variable{
				Name:               sym.Name,
				Value:              fmt.Sprintf("$%04X [%d bytes]", sym.Addr, sym.Size),
				Type:               "array",
				VariablesReference: ref,
				IndexedVariables:   count,
			})
			continue
		}
		out = append(out, Variable{
			Name:  sym.Name,
			Value: fmt.Sprintf("$%02X", s.ram.Read(sym.Addr)),
			Type:  "byte",
		})
	}
	return out
}

// arrayChildren returns the indexed byte children of an array ref, honoring
// the client's [start, start+count) paging window (count 0 = to the end).
func (s *Server) arrayChildren(ar arrayRef, start, count int) []Variable {
	if start < 0 {
		start = 0
	}
	end := ar.Count
	if count > 0 && start+count < end {
		end = start + count
	}
	if start > ar.Count {
		start = ar.Count
	}
	out := make([]Variable, 0, end-start)
	for i := start; i < end; i++ {
		addr := ar.Addr + uint16(i)
		out = append(out, Variable{
			Name:  fmt.Sprintf("[%d]", i),
			Value: fmt.Sprintf("$%02X", s.ram.Read(addr)),
			Type:  "byte",
		})
	}
	return out
}

// allocArrayRef assigns the next dynamic variablesReference for an array.
func (s *Server) allocArrayRef(addr uint16, count int) int {
	if s.varRefs == nil {
		s.varRefs = map[int]arrayRef{}
		s.varRefSeq = refDynamicBase
	}
	ref := s.varRefSeq
	s.varRefSeq++
	s.varRefs[ref] = arrayRef{Addr: addr, Count: count}
	return ref
}

// isCodeAddr reports whether addr maps to a source line — i.e. it's an
// instruction, not a data global. Returns false when no source map is loaded.
func (s *Server) isCodeAddr(addr uint16) bool {
	if s.srcMap == nil {
		return false
	}
	_, ok := s.srcMap.PCToSrc[addr]
	return ok
}

func (s *Server) regsVariables() []Variable {
	return []Variable{
		{Name: "A", Value: fmt.Sprintf("$%02X", s.cpu.A), Type: "byte"},
		{Name: "X", Value: fmt.Sprintf("$%02X", s.cpu.X), Type: "byte"},
		{Name: "Y", Value: fmt.Sprintf("$%02X", s.cpu.Y), Type: "byte"},
		{Name: "SP", Value: fmt.Sprintf("$%02X", s.cpu.SP), Type: "byte"},
		{Name: "PC", Value: fmt.Sprintf("$%04X", s.cpu.PC), Type: "word"},
		{Name: "P", Value: fmt.Sprintf("$%02X", s.cpu.P), Type: "byte"},
		{Name: "Cycles", Value: strconv.FormatUint(s.cpu.Cycles, 10), Type: "uint64"},
	}
}

func (s *Server) flagsVariables() []Variable {
	one := func(name string, bit byte) Variable {
		v := "0"
		if s.cpu.P&bit != 0 {
			v = "1"
		}
		return Variable{Name: name, Value: v, Type: "bit"}
	}
	return []Variable{
		one("N", cpu.FlagN),
		one("V", cpu.FlagV),
		one("U", cpu.FlagU),
		one("B", cpu.FlagB),
		one("D", cpu.FlagD),
		one("I", cpu.FlagI),
		one("Z", cpu.FlagZ),
		one("C", cpu.FlagC),
	}
}

func (s *Server) handleSetVariable(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee")
		return
	}
	if s.running.Load() {
		s.sendErrorResponse(req, "CPU is running; pause first")
		return
	}
	var args SetVariableArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad setVariable args: %v", err))
		return
	}
	n, err := parseDAPNumber(args.Value)
	if err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad value %q: %v", args.Value, err))
		return
	}
	type body struct {
		Value string `json:"value"`
		Type  string `json:"type,omitempty"`
	}
	switch args.VariablesReference {
	case refRegisters:
		newVal, t, err := s.setRegister(args.Name, n)
		if err != nil {
			s.sendErrorResponse(req, err.Error())
			return
		}
		s.sendResponse(req, body{Value: newVal, Type: t})
	case refFlags:
		newVal, err := s.setFlag(args.Name, n)
		if err != nil {
			s.sendErrorResponse(req, err.Error())
			return
		}
		s.sendResponse(req, body{Value: newVal, Type: "bit"})
	default:
		s.sendErrorResponse(req, fmt.Sprintf("unknown variablesReference: %d", args.VariablesReference))
	}
}

func (s *Server) setRegister(name string, v uint64) (string, string, error) {
	switch strings.ToUpper(name) {
	case "A":
		s.cpu.A = byte(v)
		return fmt.Sprintf("$%02X", s.cpu.A), "byte", nil
	case "X":
		s.cpu.X = byte(v)
		return fmt.Sprintf("$%02X", s.cpu.X), "byte", nil
	case "Y":
		s.cpu.Y = byte(v)
		return fmt.Sprintf("$%02X", s.cpu.Y), "byte", nil
	case "SP":
		s.cpu.SP = byte(v)
		return fmt.Sprintf("$%02X", s.cpu.SP), "byte", nil
	case "PC":
		s.cpu.PC = uint16(v)
		return fmt.Sprintf("$%04X", s.cpu.PC), "word", nil
	case "P":
		s.cpu.P = byte(v)
		return fmt.Sprintf("$%02X", s.cpu.P), "byte", nil
	}
	return "", "", fmt.Errorf("unknown register: %s", name)
}

func (s *Server) setFlag(name string, v uint64) (string, error) {
	var bit byte
	switch strings.ToUpper(name) {
	case "N":
		bit = cpu.FlagN
	case "V":
		bit = cpu.FlagV
	case "U":
		bit = cpu.FlagU
	case "B":
		bit = cpu.FlagB
	case "D":
		bit = cpu.FlagD
	case "I":
		bit = cpu.FlagI
	case "Z":
		bit = cpu.FlagZ
	case "C":
		bit = cpu.FlagC
	default:
		return "", fmt.Errorf("unknown flag: %s", name)
	}
	if v != 0 {
		s.cpu.P |= bit
	} else {
		s.cpu.P &^= bit
	}
	if s.cpu.P&bit != 0 {
		return "1", nil
	}
	return "0", nil
}

// parseDAPNumber accepts `$XX`, `0xXX`, decimal, or bare hex. DAP doesn't
// fix the format for setVariable values — clients (VS Code, nvim-dap)
// echo back whatever the user typed in the Variables pane.
func parseDAPNumber(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty value")
	}
	switch {
	case strings.HasPrefix(s, "$"):
		return strconv.ParseUint(s[1:], 16, 32)
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, "0X"):
		return strconv.ParseUint(s[2:], 16, 32)
	}
	if v, err := strconv.ParseUint(s, 10, 32); err == nil {
		return v, nil
	}
	return strconv.ParseUint(s, 16, 32)
}
