package tui

import (
	"encoding/base64"
	"fmt"
)

// memWindow is how many bytes the memory panel snapshots around its top-left
// address. Sized well above any panel's visible row count (64 rows × 16) so a
// single fetch always covers what memView renders; cheap to copy each sync.
const memWindow = 0x400

// memWindowFor clamps the window so a fetch anchored near the top of the
// address space doesn't run past $FFFF.
func memWindowFor(base uint16) int {
	if n := 0x10000 - int(base); n < memWindow {
		return n
	}
	return memWindow
}

// fetchMem issues one `readMemory` request for [addr, addr+count) and decodes
// the base64 payload (issue #451). Transport-agnostic via the dapRequester.
func fetchMem(c dapRequester, addr uint32, count int) ([]byte, error) {
	resp, err := c.Request("readMemory", map[string]any{
		"memoryReference": fmt.Sprintf("$%06X", addr),
		"offset":          0,
		"count":           count,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, fmt.Errorf("readMemory: %s", resp.Message)
	}
	var body struct {
		Data string `json:"data"`
	}
	if err := remarshal(resp.Body, &body); err != nil {
		return nil, fmt.Errorf("readMemory body: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		return nil, fmt.Errorf("readMemory base64 decode: %w", err)
	}
	return decoded, nil
}

// refreshMemWindow pulls the visible memory window from the active Source into
// m.MemView so memView renders DAP-sourced bytes instead of reading the core
// bus directly (issue #451). Bypasses the syncMem run-guard so the
// chippy-state fast path can call it during a remote free-run.
func (m *Model) refreshMemWindow() {
	if m.Source == nil {
		return
	}
	base := m.MemViewAddr & 0xFFF0
	base24 := uint32(m.MemViewBank)<<16 | uint32(base)
	if buf, err := m.Source.ReadMemory(base24, memWindowFor(base)); err == nil {
		m.MemView = buf
		m.MemViewBase = base
	}
}

// syncMem refreshes the memory-panel window snapshot. Mirrors syncRegs:
// skipped during a remote free-run, where the #440 dirtyRanges stream (applied
// in the chippy-state handler, which then calls refreshMemWindow) keeps the
// window live without a per-frame readMemory.
func (m *Model) syncMem() {
	if m.Source == nil {
		return
	}
	if m.Running && m.Source.Attached() {
		return
	}
	m.refreshMemWindow()
}

// memByte returns the byte at addr from the DAP-sourced window snapshot,
// falling back to 0 outside the current window (memView only reads inside it).
func (m Model) memByte(addr uint16) byte {
	off := int(addr) - int(m.MemViewBase)
	if off < 0 || off >= len(m.MemView) {
		return 0
	}
	return m.MemView[off]
}
