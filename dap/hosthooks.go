package dap

import "github.com/nkane/chippy/expr"

// Host debug hooks (issue #433) let a downstream emulator (nessy) express
// NES-aware breakpoints and step granularity through the chippy DAP server,
// keeping the core CPU-generic. They pair with AttachConfig.CustomRequestHandler
// (#416), which carries the host's own DAP requests.

// SetHostVars registers a resolver for host-defined identifiers (e.g. NES
// `scanline` / `dot` / `frame`) used by conditional-breakpoint and `evaluate`
// expressions. Pass nil to clear. The resolver's getters run at evaluation
// time, so they see live host state. Guarded by cpuMu since condition
// compilation happens under it.
func (s *Server) SetHostVars(r expr.HostVarResolver) {
	s.lockCPU()
	s.hostVars = r
	s.unlockCPU()
}

// SetStopPredicate installs (or clears, with nil) a host stop condition
// checked once per continue-loop iteration after each step. When it returns
// true the run stops with reason "step" — letting a host build run-to-NMI /
// step-scanline / step-frame on top of the server's pause/ownership model
// instead of a side-loop that bypasses it. Guarded by cpuMu.
//
// Typical use: arm the predicate, send `continue`, and disarm it (pass nil)
// when the resulting `stopped` event arrives.
func (s *Server) SetStopPredicate(pred func() bool) {
	s.lockCPU()
	s.stopPredicate = pred
	s.unlockCPU()
}
