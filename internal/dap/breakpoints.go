package dap

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// handleSetBreakpoints replaces all source-line breakpoints for the named
// source file. Per DAP semantics this is destructive against any prior
// bps for the same source; bps in other sources are unaffected.
//
// Resolution walks the loaded source map (the .dbg file's PC -> file:line
// table, reversed). Matching is on basename so editors that pass an
// absolute project-root path resolve against a .dbg that records bare
// filenames. A bp is reported `verified: false` when no PC matches.
func (s *Server) handleSetBreakpoints(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args SetBreakpointsArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad setBreakpoints args: %v", err))
		return
	}

	srcKey := canonicalSourcePath(args.Source)
	resolved := make(map[int]uint16)
	results := make([]Breakpoint, 0, len(args.Breakpoints))
	for _, bp := range args.Breakpoints {
		pc, ok := s.lookupSourceLine(args.Source, bp.Line)
		if !ok {
			results = append(results, Breakpoint{
				Verified: false,
				Line:     bp.Line,
				Message:  fmt.Sprintf("no PC for %s:%d", srcKey, bp.Line),
			})
			continue
		}
		resolved[bp.Line] = pc
		results = append(results, Breakpoint{
			Verified:                    true,
			Line:                        bp.Line,
			Source:                      &args.Source,
			InstructionPointerReference: fmt.Sprintf("$%04X", pc),
		})
	}

	s.bpMu.Lock()
	if len(resolved) == 0 {
		delete(s.bpsBySrc, srcKey)
	} else {
		s.bpsBySrc[srcKey] = resolved
	}
	s.rebuildBPHit()
	s.bpMu.Unlock()

	type body struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	s.sendResponse(req, body{Breakpoints: results})
}

// handleSetInstructionBreakpoints replaces all address breakpoints in one
// call. The instructionReference field is parsed as a hex address — $XX,
// 0xXX, or bare hex — matching parseDAPNumber's grammar.
func (s *Server) handleSetInstructionBreakpoints(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args SetInstructionBreakpointsArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad setInstructionBreakpoints args: %v", err))
		return
	}

	newInst := map[uint16]bool{}
	results := make([]Breakpoint, 0, len(args.Breakpoints))
	for _, bp := range args.Breakpoints {
		n, err := parseDAPNumber(bp.InstructionReference)
		if err != nil {
			results = append(results, Breakpoint{
				Verified: false,
				Message:  fmt.Sprintf("bad instructionReference %q: %v", bp.InstructionReference, err),
			})
			continue
		}
		pc := uint16(int(n) + bp.Offset)
		newInst[pc] = true
		results = append(results, Breakpoint{
			Verified:                    true,
			InstructionPointerReference: fmt.Sprintf("$%04X", pc),
		})
	}

	s.bpMu.Lock()
	s.bpsInst = newInst
	s.rebuildBPHit()
	s.bpMu.Unlock()

	type body struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	s.sendResponse(req, body{Breakpoints: results})
}

// rebuildBPHit flattens bpsBySrc + bpsInst into the single PC set the run
// loop checks. Call under bpMu.
func (s *Server) rebuildBPHit() {
	hit := make(map[uint16]bool, len(s.bpsInst))
	for pc := range s.bpsInst {
		hit[pc] = true
	}
	for _, lineMap := range s.bpsBySrc {
		for _, pc := range lineMap {
			hit[pc] = true
		}
	}
	s.bpHit = hit
}

// isBreakpoint reports whether pc has a breakpoint pending. Run loop hot
// path; takes bpMu only long enough to read the map.
func (s *Server) isBreakpoint(pc uint16) bool {
	s.bpMu.Lock()
	defer s.bpMu.Unlock()
	return s.bpHit[pc]
}

// lookupSourceLine maps (source, line) -> PC using the loaded .dbg source
// map. Returns (0, false) when the source map is absent or the line has
// no recorded PC.
func (s *Server) lookupSourceLine(src Source, line int) (uint16, bool) {
	if s.srcMap == nil {
		return 0, false
	}
	want := canonicalSourcePath(src)
	for pc, loc := range s.srcMap.PCToSrc {
		if loc.Line != line {
			continue
		}
		if matchesSource(loc.File, src.Path, want) {
			return pc, true
		}
	}
	return 0, false
}

// canonicalSourcePath returns the form we key bpsBySrc under. Prefer the
// full path when the client sent one; otherwise fall back to the name.
func canonicalSourcePath(src Source) string {
	if src.Path != "" {
		return src.Path
	}
	return src.Name
}

// matchesSource decides whether a .dbg-recorded file equals the source
// the client is asking about. We're permissive: equal path, equal
// basename, or basename of the .dbg entry matches the canonical key.
func matchesSource(dbgFile, clientPath, canonicalKey string) bool {
	if dbgFile == clientPath {
		return true
	}
	if filepath.Base(dbgFile) == filepath.Base(clientPath) {
		return true
	}
	if filepath.Base(dbgFile) == filepath.Base(canonicalKey) {
		return true
	}
	if strings.EqualFold(filepath.Base(dbgFile), filepath.Base(clientPath)) {
		return true
	}
	return false
}
