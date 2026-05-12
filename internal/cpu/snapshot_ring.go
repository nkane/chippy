package cpu

// DefaultSnapshotRingCap is the ring buffer size used by both the TUI's
// rewind feature and the DAP server's stepBack handler. 256 snapshots ×
// 64 KiB RAM = 16 MiB worst-case, well within budget for a debugger.
const DefaultSnapshotRingCap = 256

// SnapshotRing is a fixed-size FIFO of CPU snapshots. Push overwrites the
// oldest entry when full; Pop removes the most-recently-pushed entry
// (LIFO for reverse-step semantics — the most recent forward step is
// undone first). Nil receiver methods are safe — a nil ring acts like a
// zero-capacity ring that drops every Push and returns nothing.
type SnapshotRing struct {
	buf  []Snapshot
	head int // next-write index
	size int // filled count, ≤ cap
	cap  int
}

// NewSnapshotRing constructs a ring with the given capacity. cap <= 0
// yields nil so callers can disable the feature without special-casing.
func NewSnapshotRing(cap int) *SnapshotRing {
	if cap <= 0 {
		return nil
	}
	return &SnapshotRing{buf: make([]Snapshot, cap), cap: cap}
}

func (r *SnapshotRing) Push(s Snapshot) {
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
func (r *SnapshotRing) Pop() (Snapshot, bool) {
	if r == nil || r.size == 0 {
		return Snapshot{}, false
	}
	r.head = (r.head - 1 + r.cap) % r.cap
	s := r.buf[r.head]
	r.size--
	return s, true
}

func (r *SnapshotRing) Len() int {
	if r == nil {
		return 0
	}
	return r.size
}

// Reset drops all snapshots without freeing the backing buffer.
func (r *SnapshotRing) Reset() {
	if r == nil {
		return
	}
	r.head = 0
	r.size = 0
}
