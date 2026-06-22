package dap

import (
	"encoding/json"
	"fmt"

	"github.com/nkane/chippy/cpu"
)

// dataBP is one installed watchpoint: the access that triggers it plus the
// optional condition / hit-count / log meta (shared with instruction bps).
type dataBP struct {
	access DataBreakpointAccessType
	meta   *bpMeta
}

// matches reports whether a bus access of kind should trigger this watchpoint.
// Opcode fetches (AccessExec) never trigger a data breakpoint.
func (d *dataBP) matches(kind cpu.AccessKind) bool {
	switch kind {
	case cpu.AccessRead:
		return d.access == DataAccessRead || d.access == DataAccessReadWrite
	case cpu.AccessWrite:
		return d.access == DataAccessWrite || d.access == DataAccessReadWrite
	}
	return false
}

// resolveDataAddr maps a dataBreakpointInfo / dataId name to an address: a hex
// ("$XXXX", "0xXX") or decimal literal, else a loaded symbol name.
func (s *Server) resolveDataAddr(name string) (uint16, bool) {
	if n, err := parseDAPNumber(name); err == nil && n <= 0xFFFF {
		return uint16(n), true
	}
	if s.syms != nil {
		if addr, ok := s.syms.LookupName(name); ok {
			return addr, true
		}
	}
	return 0, false
}

// handleDataBreakpointInfo resolves a memory reference / symbol to a data
// breakpoint id (issue #453). chippy uses the resolved "$XXXX" address as the
// id and reports all three access types as available.
func (s *Server) handleDataBreakpointInfo(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args DataBreakpointInfoArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad dataBreakpointInfo args: %v", err))
		return
	}
	type body struct {
		DataID      *string                    `json:"dataId"`
		Description string                     `json:"description"`
		AccessTypes []DataBreakpointAccessType `json:"accessTypes,omitempty"`
		CanPersist  bool                       `json:"canPersist"`
	}
	addr, ok := s.resolveDataAddr(args.Name)
	if !ok {
		s.sendResponse(req, body{
			DataID:      nil,
			Description: fmt.Sprintf("not a known address or symbol: %q", args.Name),
		})
		return
	}
	id := fmt.Sprintf("$%04X", addr)
	s.sendResponse(req, body{
		DataID:      &id,
		Description: id,
		AccessTypes: []DataBreakpointAccessType{DataAccessRead, DataAccessWrite, DataAccessReadWrite},
		CanPersist:  false,
	})
}

// handleSetDataBreakpoints replaces the full watchpoint set (issue #453). Each
// entry's dataId is the "$XXXX" address from dataBreakpointInfo; accessType
// defaults to write. Conditions / hit counts / log messages reuse the
// instruction-breakpoint meta machinery.
func (s *Server) handleSetDataBreakpoints(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args SetDataBreakpointsArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad setDataBreakpoints args: %v", err))
		return
	}
	newBPs := map[uint16]*dataBP{}
	results := make([]Breakpoint, 0, len(args.Breakpoints))
	for _, bp := range args.Breakpoints {
		addr, ok := s.resolveDataAddr(bp.DataID)
		if !ok {
			results = append(results, Breakpoint{
				Verified: false,
				Message:  fmt.Sprintf("bad dataId %q", bp.DataID),
			})
			continue
		}
		access := bp.AccessType
		if access == "" {
			access = DataAccessWrite
		}
		meta, err := s.buildBPMeta(bp.Condition, bp.HitCondition, bp.LogMessage)
		if err != nil {
			results = append(results, Breakpoint{Verified: false, Message: err.Error()})
			continue
		}
		newBPs[addr] = &dataBP{access: access, meta: meta}
		results = append(results, Breakpoint{Verified: true})
	}

	s.bpMu.Lock()
	s.dataBPs = newBPs
	s.bpMu.Unlock()

	type body struct {
		Breakpoints []Breakpoint `json:"breakpoints"`
	}
	s.sendResponse(req, body{Breakpoints: results})
}

// dataBPMeta returns the meta for the watchpoint at addr, or nil.
func (s *Server) dataBPMeta(addr uint16) *bpMeta {
	if bp := s.dataBPs[addr]; bp != nil {
		return bp.meta
	}
	return nil
}
