// Package tui — bus wrapper that records memory access hits.
//
// WBus sits between the CPU and the underlying RAM (or any cpu.Bus). Each
// Read/Write checks an addr->MemBP map and, if the access matches a watch,
// pushes a Hit onto an internal queue. The TUI run-loop drains the queue
// after every Step() and decides whether to pause / log / increment counters.
//
// Design notes:
//   - The map is owned by the Model (not WBus) so :bpr / :bpw command edits
//     don't need any cross-goroutine synchronization. WBus just holds a
//     pointer to the same map header.
//   - Hits are pushed to a fixed-capacity ring; if the CPU somehow generates
//     more hits between drains than the ring holds, oldest entries are
//     dropped. This is fine for a debugger — pause-on-hit semantics still
//     fire on the first one that matters.
//   - Hot-path branch: if the map is empty we skip the lookup entirely.
package tui

import "github.com/nkane/chippy/internal/cpu"

// MemHit is one recorded access to a watched address.
type MemHit struct {
	Addr   uint16
	Kind   MemBPKind // "r" or "w" (never "rw" — that's the watch kind, not the access)
	Value  byte      // value read or written
	PC     uint16    // CPU PC at moment of access
	Cycles uint64    // CPU cycle count at moment of access
}

// hitRingCap is the queue depth between drains. A single 6502 instruction
// performs at most ~7 memory accesses, so 64 leaves plenty of headroom even
// if the run loop falls behind for a few hundred cycles.
const hitRingCap = 64

// WBus wraps an underlying cpu.Bus and reports watched accesses.
type WBus struct {
	Inner cpu.Bus

	// Pointer back to the Model so Read/Write can capture PC and Cycles
	// without extra plumbing. Set once at construction; do not mutate.
	cpu *cpu.CPU

	// Watches points at Model.MemBPs. nil or empty map -> hot-path skips.
	Watches map[uint16]*MemBP

	// Ring buffer of pending hits.
	hits  [hitRingCap]MemHit
	head  int // next write
	count int // number of valid entries
}

// NewWBus wraps inner. The CPU pointer is needed so hits carry PC/cycle
// context; pass it after cpu.New(wbus) returns.
func NewWBus(inner cpu.Bus) *WBus {
	return &WBus{Inner: inner}
}

// AttachCPU stores the CPU pointer used for hit context. Call once after
// cpu.New(wbus) so the WBus can record PC/Cycles on each hit.
func (w *WBus) AttachCPU(c *cpu.CPU) { w.cpu = c }

// SetWatches points the wrapper at the live map. Pass Model.MemBPs directly;
// edits to that map are seen by the wrapper without re-attach.
func (w *WBus) SetWatches(m map[uint16]*MemBP) { w.Watches = m }

func (w *WBus) Read(addr uint16) byte {
	v := w.Inner.Read(addr)
	if len(w.Watches) > 0 {
		if bp, ok := w.Watches[addr]; ok && bp.matches(MemBPRead) {
			w.push(MemHit{Addr: addr, Kind: MemBPRead, Value: v, PC: w.pc(), Cycles: w.cyc()})
		}
	}
	return v
}

func (w *WBus) Write(addr uint16, v byte) {
	w.Inner.Write(addr, v)
	if len(w.Watches) > 0 {
		if bp, ok := w.Watches[addr]; ok && bp.matches(MemBPWrite) {
			w.push(MemHit{Addr: addr, Kind: MemBPWrite, Value: v, PC: w.pc(), Cycles: w.cyc()})
		}
	}
}

func (w *WBus) push(h MemHit) {
	w.hits[w.head] = h
	w.head = (w.head + 1) % hitRingCap
	if w.count < hitRingCap {
		w.count++
	}
}

// Drain returns up to all currently pending hits in FIFO order and clears
// the ring. Caller is the Model's run-loop.
func (w *WBus) Drain() []MemHit {
	if w.count == 0 {
		return nil
	}
	out := make([]MemHit, w.count)
	start := (w.head - w.count + hitRingCap) % hitRingCap
	for i := 0; i < w.count; i++ {
		out[i] = w.hits[(start+i)%hitRingCap]
	}
	w.count = 0
	return out
}

func (w *WBus) pc() uint16 {
	if w.cpu == nil {
		return 0
	}
	return w.cpu.PC
}

func (w *WBus) cyc() uint64 {
	if w.cpu == nil {
		return 0
	}
	return w.cpu.Cycles
}
