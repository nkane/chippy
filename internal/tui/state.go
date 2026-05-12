package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// StateSchemaVersion is the current persistence-format version. Bumped
// only when a field's *meaning* changes incompatibly — pure additions
// stay on v1 because absent fields decode as their zero value.
//
// Compatibility rules for v1:
//   - A v1 writer must include this field.
//   - A v1+ reader accepts: schemaVersion absent (treated as v0 legacy),
//     schemaVersion == 1 (current), or schemaVersion > 1 (silently
//     skipped — older chippy must not corrupt a newer file).
//   - New fields added inside v1.x stay optional; loaders fill defaults.
//   - Field removals or semantic changes require a new SchemaVersion.
const StateSchemaVersion = 1

// savedState is what we persist to ~/.chippy/state-<rom>.json.
//
// Breakpoints uses json.RawMessage so we can accept either the new shape
// ([]Breakpoint) or the legacy shape ([]uint16). New writes always use the
// rich form.
type savedState struct {
	SchemaVersion int             `json:"schemaVersion,omitempty"`
	Breakpoints   json.RawMessage `json:"breakpoints,omitempty"`
	MemBPs        json.RawMessage `json:"mem_bps,omitempty"`
	MemViewAddr   uint16          `json:"mem_view_addr"`
	MemCursor     uint16          `json:"mem_cursor,omitempty"`
	Watches       []Watch         `json:"watches"`
	TargetHz      int             `json:"target_hz"`

	// v1.x additions (issue #125). These fields didn't exist on v0
	// files; the loader gates them on SchemaVersion so legacy files
	// keep the New(c, r) defaults instead of decoding to false/zero.
	DisasmFollow     *bool            `json:"disasm_follow,omitempty"`
	StackAnnotate    *bool            `json:"stack_annotate,omitempty"`
	InputMode        bool             `json:"input_mode,omitempty"`
	DisasmAnchor     uint16           `json:"disasm_anchor,omitempty"`
	ImmediateHistory []ImmediateEntry `json:"immediate_history,omitempty"`
}

func loadState(m *Model, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var s savedState
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}
	// schemaVersion == 0 means a legacy file written before the freeze
	// (issue #112). Those used the same field names so the unmarshal
	// above already populated everything; we just keep loading.
	// schemaVersion > current means a future chippy wrote this file.
	// Skip — preserves the user's state for the newer build instead of
	// silently downgrading it.
	if s.SchemaVersion > StateSchemaVersion {
		return
	}
	m.MemViewAddr = s.MemViewAddr
	m.MemCursor = s.MemCursor
	m.Watches = s.Watches
	m.TargetHz = s.TargetHz
	// v1.x additions (issue #125). Gated on SchemaVersion so a legacy
	// v0 file doesn't clobber the New(c, r) defaults with zero values.
	if s.SchemaVersion >= 1 {
		if s.DisasmFollow != nil {
			m.DisasmFollow = *s.DisasmFollow
		}
		if s.StackAnnotate != nil {
			m.StackAnnotate = *s.StackAnnotate
		}
		m.InputMode = s.InputMode
		m.DisasmAnchor = s.DisasmAnchor
		m.ImmediateHistory = s.ImmediateHistory
	}
	if m.Breakpoints == nil {
		m.Breakpoints = make(map[uint16]*Breakpoint)
	}
	if len(s.Breakpoints) > 0 {
		// Try new shape first.
		var bps []Breakpoint
		if err := json.Unmarshal(s.Breakpoints, &bps); err == nil && looksLikeBPArray(s.Breakpoints) {
			for i := range bps {
				bp := bps[i]
				b := bp // copy
				if b.Cond != "" {
					if fn, cerr := compileCondition(b.Cond, m.Syms); cerr == nil {
						b.condFn = fn
					} else {
						// Bad cond on disk: mark rejected so user sees it.
						b.Rejected = true
					}
				}
				m.Breakpoints[b.Addr] = &b
			}
		} else {
			// Fall back to legacy []uint16.
			var legacy []uint16
			if err := json.Unmarshal(s.Breakpoints, &legacy); err == nil {
				for _, a := range legacy {
					m.Breakpoints[a] = newBP(a)
				}
			}
		}
	}
	loadMemBPs(m, s.MemBPs)
}

// loadMemBPs populates m.MemBPs from the persisted JSON. Same condition
// recompile dance as the exec breakpoint loader.
func loadMemBPs(m *Model, raw json.RawMessage) {
	if m.MemBPs == nil {
		m.MemBPs = make(map[uint16]*MemBP)
	}
	if len(raw) == 0 {
		return
	}
	var bps []MemBP
	if err := json.Unmarshal(raw, &bps); err != nil {
		return
	}
	for i := range bps {
		b := bps[i]
		if b.Cond != "" {
			if fn, cerr := compileCondition(b.Cond, m.Syms); cerr == nil {
				b.condFn = fn
			}
		}
		m.MemBPs[b.Addr] = &b
	}
}

// looksLikeBPArray returns true if the raw json is an array whose first
// element is an object (new shape) rather than a number (legacy shape).
func looksLikeBPArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r', '[':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

func (m *Model) saveState() {
	if m.StatePath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.StatePath), 0o755); err != nil {
		return
	}
	bps := make([]Breakpoint, 0, len(m.Breakpoints))
	for _, bp := range m.Breakpoints {
		if bp == nil {
			continue
		}
		bps = append(bps, *bp)
	}
	raw, err := json.Marshal(bps)
	if err != nil {
		return
	}
	mbps := make([]MemBP, 0, len(m.MemBPs))
	for _, bp := range m.MemBPs {
		if bp == nil {
			continue
		}
		mbps = append(mbps, *bp)
	}
	mraw, err := json.Marshal(mbps)
	if err != nil {
		return
	}
	df, sa := m.DisasmFollow, m.StackAnnotate
	s := savedState{
		SchemaVersion:    StateSchemaVersion,
		Breakpoints:      raw,
		MemBPs:           mraw,
		MemViewAddr:      m.MemViewAddr,
		MemCursor:        m.MemCursor,
		Watches:          m.Watches,
		TargetHz:         m.TargetHz,
		DisasmFollow:     &df,
		StackAnnotate:    &sa,
		InputMode:        m.InputMode,
		DisasmAnchor:     m.DisasmAnchor,
		ImmediateHistory: m.ImmediateHistory,
	}
	data, err := json.MarshalIndent(&s, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(m.StatePath, data, 0o644)
}

// DefaultStatePath returns ~/.chippy/state-<basename>.json for a given ROM path.
func DefaultStatePath(romPath string) string {
	if romPath == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	base := filepath.Base(romPath)
	return filepath.Join(home, ".chippy", "state-"+base+".json")
}
