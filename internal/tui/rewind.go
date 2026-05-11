package tui

import "github.com/nkane/chippy/internal/cpu"

// defaultRewindCap is the ring buffer size. 256 snapshots × 64 KiB RAM = 16
// MiB worst-case, well within budget for a debugger.
const defaultRewindCap = 256

// rewindRing is a fixed-size FIFO of CPU snapshots. Push overwrites the
// oldest entry when full; Pop removes the most-recently-pushed entry (LIFO
// for reverse-step semantics — most recent forward step is undone first).
type rewindRing struct {
	buf  []cpu.Snapshot
	head int // next-write index
	size int // filled count, ≤ cap
	cap  int
}

func newRewindRing(cap int) *rewindRing {
	if cap <= 0 {
		return nil
	}
	return &rewindRing{buf: make([]cpu.Snapshot, cap), cap: cap}
}

func (r *rewindRing) Push(s cpu.Snapshot) {
	if r == nil || r.cap == 0 {
		return
	}
	r.buf[r.head] = s
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

// Pop removes and returns the most recently pushed snapshot. (Snapshot{},
// false) when empty.
func (r *rewindRing) Pop() (cpu.Snapshot, bool) {
	if r == nil || r.size == 0 {
		return cpu.Snapshot{}, false
	}
	r.head = (r.head - 1 + r.cap) % r.cap
	s := r.buf[r.head]
	r.size--
	return s, true
}

func (r *rewindRing) Len() int {
	if r == nil {
		return 0
	}
	return r.size
}

// Reset drops all snapshots without freeing the backing buffer.
func (r *rewindRing) Reset() {
	if r == nil {
		return
	}
	r.head = 0
	r.size = 0
}
