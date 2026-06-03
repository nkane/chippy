package cpu

// Keyframe-based deep rewind (issue #392).
//
// The per-step SnapshotRing only reaches back as far as its capacity (a few
// hundred steps) — fine for "oops, step back one" but useless for "rewind to
// somewhere in the last few million steps". Storing a delta for every one of
// those steps is infeasible, so deep rewind instead keeps periodic *full*
// machine snapshots (keyframes) and reconstructs an arbitrary earlier state
// by restoring the nearest keyframe at or before the target step and
// replaying forward the handful of steps in between.
//
//	reach (steps) = ring capacity (keyframes) × keyframe interval (steps)
//	ring capacity = budget bytes / KeyframeBytes
//
// Memory is a *cap*, not a preallocation: a short run holds only as many
// keyframes as it produced. Forward-replay cost is bounded by the interval,
// so a larger interval trades replay latency for reach at a fixed budget.

// KeyframeBytes is the accounting size of one keyframe: a full 64 KiB RAM
// image. Register/peripheral state is negligible next to it, so the budget
// math treats every keyframe as this fixed size.
const KeyframeBytes = 0x10000

// Keyframe is a full machine snapshot tagged with the step index at which it
// was taken. Snap.Pages holds every page (a complete RAM image), so Restore
// reconstructs the exact state with no delta chain.
type Keyframe struct {
	Step uint64
	Snap Snapshot
}

// SnapshotFull captures a complete RAM image (all 256 pages) plus registers,
// suitable for use as a keyframe base. Unlike CPU.Snapshot — which records
// only a page delta for undoing a single step — this is self-contained:
// Restore needs nothing else. Peripherals are filled in by the caller, as
// with the delta path.
func (c *CPU) SnapshotFull(ram *RAM) Snapshot {
	s := c.Snapshot(ram)
	pages := make(map[byte][256]byte, 256)
	for p := 0; p < 256; p++ {
		var img [256]byte
		base := p << 8
		copy(img[:], ram.Data[base:base+256])
		pages[byte(p)] = img
	}
	s.Pages = pages
	return s
}

// KeyframeRing is a fixed-capacity FIFO of keyframes ordered by ascending
// step. Push appends the newest; when full it drops the oldest, so the ring
// always holds the most recent `cap` keyframes. Nil receiver methods are
// safe and behave as an empty, zero-capacity ring.
type KeyframeRing struct {
	buf  []Keyframe
	head int // next-write index
	size int
	cap  int
}

// NewKeyframeRing builds a ring sized to hold budgetBytes worth of keyframes.
// A budget too small for even one keyframe still yields a 1-slot ring so deep
// rewind degrades to "nearest keyframe" rather than disabling outright; a
// non-positive budget yields nil (feature off).
func NewKeyframeRing(budgetBytes int) *KeyframeRing {
	if budgetBytes <= 0 {
		return nil
	}
	c := budgetBytes / KeyframeBytes
	if c < 1 {
		c = 1
	}
	return &KeyframeRing{buf: make([]Keyframe, c), cap: c}
}

// Cap returns the ring's keyframe capacity (0 for a nil ring).
func (r *KeyframeRing) Cap() int {
	if r == nil {
		return 0
	}
	return r.cap
}

// Len returns the number of keyframes currently held.
func (r *KeyframeRing) Len() int {
	if r == nil {
		return 0
	}
	return r.size
}

// Bytes is the approximate resident size of the held keyframes.
func (r *KeyframeRing) Bytes() int {
	return r.Len() * KeyframeBytes
}

// Push appends a keyframe. Callers are responsible for pushing in ascending
// step order (the TUI does, since it captures during forward execution).
func (r *KeyframeRing) Push(kf Keyframe) {
	if r == nil || r.cap == 0 {
		return
	}
	r.buf[r.head] = kf
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
}

// Nearest returns the latest keyframe whose Step is <= target, and true. When
// the ring is empty or every held keyframe is newer than target (target fell
// off the back of the reach window), it returns false.
func (r *KeyframeRing) Nearest(target uint64) (Keyframe, bool) {
	if r == nil || r.size == 0 {
		return Keyframe{}, false
	}
	// Entries run oldest..newest starting at (head - size). Scan newest-first
	// and take the first with Step <= target.
	for i := 0; i < r.size; i++ {
		idx := (r.head - 1 - i + r.cap) % r.cap
		if r.buf[idx].Step <= target {
			return r.buf[idx], true
		}
	}
	return Keyframe{}, false
}

// Oldest returns the lowest step still reachable (the back of the window) and
// true, or (0,false) when empty. Used to report reach to the user.
func (r *KeyframeRing) Oldest() (uint64, bool) {
	if r == nil || r.size == 0 {
		return 0, false
	}
	idx := (r.head - r.size + r.cap) % r.cap
	return r.buf[idx].Step, true
}

// Reset drops all keyframes without freeing the backing buffer.
func (r *KeyframeRing) Reset() {
	if r == nil {
		return
	}
	r.head = 0
	r.size = 0
}
