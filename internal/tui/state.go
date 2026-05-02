package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type savedState struct {
	Breakpoints []uint16 `json:"breakpoints"`
	MemViewAddr uint16   `json:"mem_view_addr"`
	Watches     []Watch  `json:"watches"`
	TargetHz    int      `json:"target_hz"`
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
	for _, a := range s.Breakpoints {
		m.Breakpoints[a] = true
	}
	m.MemViewAddr = s.MemViewAddr
	m.Watches = s.Watches
	m.TargetHz = s.TargetHz
}

func (m *Model) saveState() {
	if m.StatePath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(m.StatePath), 0o755); err != nil {
		return
	}
	bps := make([]uint16, 0, len(m.Breakpoints))
	for a := range m.Breakpoints {
		bps = append(bps, a)
	}
	s := savedState{
		Breakpoints: bps,
		MemViewAddr: m.MemViewAddr,
		Watches:     m.Watches,
		TargetHz:    m.TargetHz,
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
