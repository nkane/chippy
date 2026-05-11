package dap

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nkane/chippy/internal/cpu"
)

// Variable-reference IDs. Static / well-known because the 6502 has a
// single fixed set of registers and flags; no per-frame variation. Real
// debug targets typically allocate these dynamically, but for 6502 a
// constant map is plain and lets `setVariable` switch on the ref ID
// instead of carrying a generation counter.
const (
	refRegisters = 1
	refFlags     = 2
)

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
		retAddr, _, ok := cpu.DetectStackFrame(s.ram, sp)
		if !ok {
			a++
			continue
		}
		// retAddr is the address RTS will jump to. The caller's PC at the
		// JSR site is retAddr - 1 (the operand byte of the JSR). For the
		// stack-trace `Name` we still report retAddr so the user sees
		// where the routine will resume.
		frames = append(frames, s.frameForPC(len(frames), retAddr))
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
	s.sendResponse(req, body{Scopes: []Scope{
		{Name: "Registers", VariablesReference: refRegisters, Expensive: false},
		{Name: "Flags", VariablesReference: refFlags, Expensive: false},
	}})
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
	default:
		s.sendErrorResponse(req, fmt.Sprintf("unknown variablesReference: %d", args.VariablesReference))
	}
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
