package dap

import (
	"encoding/json"
	"fmt"
)

// handleSetExceptionBreakpoints toggles chippy's "pause on BRK" mode.
// The filter ID "brk" is the only one we advertise (declared in
// handleInitialize). Any other filter the client sends is silently
// ignored; the spec lets servers reject unknown filters but VS Code in
// particular will pass an empty list to clear, so we accept gracefully.
func (s *Server) handleSetExceptionBreakpoints(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args SetExceptionBreakpointsArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad setExceptionBreakpoints args: %v", err))
		return
	}
	on := false
	for _, f := range args.Filters {
		if f == "brk" {
			on = true
			break
		}
	}
	s.brkOnException.Store(on)
	s.sendResponse(req, nil)
}

// handleExceptionInfo describes the most recently fired exception.
// Currently chippy only surfaces BRK; the response leans on
// `lastExceptionPC` written by the run loop when the exception fires.
func (s *Server) handleExceptionInfo(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee")
		return
	}
	pc := uint16(s.lastExceptionPC.Load())
	s.sendResponse(req, ExceptionInfoResponseBody{
		ExceptionID: "brk",
		Description: fmt.Sprintf("BRK at $%04X", pc),
		BreakMode:   "always",
	})
}
