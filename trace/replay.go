// Package trace parses chippy's execution-trace text format back into
// a navigable sequence of Frames (issue #64). A Replay can then drive
// a TUI view that scrolls forward/backward through a recorded run —
// the post-mortem analog of the live reverse-step ring.
package trace

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Frame is one row in a parsed trace. Matches the columns FileTracer
// writes:
//
//	8000  A9 42         LDA #$42           A:00 X:00 Y:00 P:24 SP:FD CYC:7
type Frame struct {
	PC          uint16
	OpBytes     []byte
	Mnemonic    string
	A, X, Y     byte
	P, SP       byte
	Cycles      uint64
	InterruptIn string // "NMI" / "IRQ" / "" for normal instruction frames
}

// Replay is a parsed trace plus a cursor.
type Replay struct {
	Frames []Frame
	Index  int
}

// Parse consumes an entire trace from r into a Replay. Empty lines are
// skipped. Lines starting with "----" are interrupt boundary markers;
// they decorate the *next* Frame's InterruptIn field rather than
// producing their own frame.
func Parse(r io.Reader) (*Replay, error) {
	rep := &Replay{}
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	lineNo := 0
	var pendingInterrupt string
	for scan.Scan() {
		lineNo++
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "----") {
			// "---- NMI -> $FFFA (PC=$8042 P=24 SP=FD CYC:123)"
			kind, _ := splitInterruptLine(line)
			pendingInterrupt = kind
			continue
		}
		f, err := parseLine(line)
		if err != nil {
			return nil, fmt.Errorf("trace: line %d: %w", lineNo, err)
		}
		f.InterruptIn = pendingInterrupt
		pendingInterrupt = ""
		rep.Frames = append(rep.Frames, f)
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("trace: read: %w", err)
	}
	return rep, nil
}

// Len returns the frame count.
func (r *Replay) Len() int { return len(r.Frames) }

// Current returns the frame at the cursor (or false if the replay is empty).
func (r *Replay) Current() (Frame, bool) {
	if r == nil || len(r.Frames) == 0 {
		return Frame{}, false
	}
	if r.Index < 0 {
		r.Index = 0
	}
	if r.Index >= len(r.Frames) {
		r.Index = len(r.Frames) - 1
	}
	return r.Frames[r.Index], true
}

// Step advances the cursor by n (negative goes back). Returns true if
// the cursor moved.
func (r *Replay) Step(n int) bool {
	if r == nil || len(r.Frames) == 0 {
		return false
	}
	prev := r.Index
	r.Index += n
	if r.Index < 0 {
		r.Index = 0
	}
	if r.Index >= len(r.Frames) {
		r.Index = len(r.Frames) - 1
	}
	return r.Index != prev
}

// Seek jumps the cursor to the first frame whose PC matches addr.
// Returns true if a match was found.
func (r *Replay) Seek(addr uint16) bool {
	if r == nil {
		return false
	}
	for i, f := range r.Frames {
		if f.PC == addr {
			r.Index = i
			return true
		}
	}
	return false
}

// SeekCycle moves the cursor to the first frame whose Cycles >= target and
// returns true. Trace cycle counts are monotonic non-decreasing, so this is
// a binary search — O(log N) on a million-frame trace (issue #391). If every
// frame is below target the cursor lands on the last frame and it returns
// false (target past end).
func (r *Replay) SeekCycle(target uint64) bool {
	if r == nil || len(r.Frames) == 0 {
		return false
	}
	lo, hi := 0, len(r.Frames) // first index with Cycles >= target
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if r.Frames[mid].Cycles < target {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(r.Frames) {
		r.Index = len(r.Frames) - 1
		return false
	}
	r.Index = lo
	return true
}

// FindFunc scans for the next frame satisfying pred, starting one step away
// from `from` in direction dir (+1 forward, -1 backward). Returns the match
// index and true, or (from, false) when none is found. The cursor is NOT
// moved — callers decide whether to commit the jump.
func (r *Replay) FindFunc(pred func(Frame) bool, from, dir int) (int, bool) {
	if r == nil || len(r.Frames) == 0 || pred == nil {
		return from, false
	}
	if dir >= 0 {
		dir = 1
	} else {
		dir = -1
	}
	for i := from + dir; i >= 0 && i < len(r.Frames); i += dir {
		if pred(r.Frames[i]) {
			return i, true
		}
	}
	return from, false
}

// Equal reports whether two frames represent the same architectural state:
// PC, the visible registers, and the cycle count. OpBytes / Mnemonic are
// derived from PC so they're excluded; InterruptIn is excluded because a
// divergence in interrupt timing already shows up as a Cycles/PC mismatch.
func (f Frame) Equal(g Frame) bool {
	return f.PC == g.PC &&
		f.A == g.A && f.X == g.X && f.Y == g.Y &&
		f.P == g.P && f.SP == g.SP &&
		f.Cycles == g.Cycles
}

// Divergence is the first frame index at which two replays of the same ROM
// stop agreeing. Found is false when the traces match for their entire
// overlapping length.
type Divergence struct {
	Index int    // frame index in both replays where they first differ
	Cycle uint64 // Cycles of the left replay at Index (the divergence point)
	Found bool
}

// Diff compares two replays frame-by-frame by index and reports the first
// divergence. A length mismatch with no earlier state difference counts as a
// divergence at the end of the shorter trace (one ran longer than the
// other). Used by `-diff` mode to mark where two builds parted ways.
func Diff(a, b *Replay) Divergence {
	if a == nil || b == nil {
		return Divergence{}
	}
	n := a.Len()
	if b.Len() < n {
		n = b.Len()
	}
	for i := 0; i < n; i++ {
		if !a.Frames[i].Equal(b.Frames[i]) {
			return Divergence{Index: i, Cycle: a.Frames[i].Cycles, Found: true}
		}
	}
	if a.Len() != b.Len() {
		return Divergence{Index: n, Cycle: lenCycle(a, b, n), Found: true}
	}
	return Divergence{}
}

// lenCycle returns the cycle count at the divergence-by-length point: the
// first frame past the shorter trace, taken from whichever replay still has
// it.
func lenCycle(a, b *Replay, n int) uint64 {
	if n < a.Len() {
		return a.Frames[n].Cycles
	}
	if n < b.Len() {
		return b.Frames[n].Cycles
	}
	return 0
}

// parseLine pulls one Frame out of a trace row. The format is
// fixed-column-ish but tolerant of variable whitespace.
func parseLine(line string) (Frame, error) {
	// Split on whitespace then walk fields in order.
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return Frame{}, fmt.Errorf("too few fields: %q", line)
	}
	var f Frame
	pc, err := strconv.ParseUint(fields[0], 16, 16)
	if err != nil {
		return Frame{}, fmt.Errorf("bad PC %q: %w", fields[0], err)
	}
	f.PC = uint16(pc)

	// fields[1..N] are 1–3 opcode bytes (each one or two hex chars).
	// The mnemonic follows. We pull bytes while each token is a two-char
	// hex string.
	i := 1
	for i < len(fields) && isHexByte(fields[i]) {
		b, err := strconv.ParseUint(fields[i], 16, 8)
		if err != nil {
			break
		}
		f.OpBytes = append(f.OpBytes, byte(b))
		i++
	}
	if i >= len(fields) {
		return Frame{}, fmt.Errorf("missing mnemonic: %q", line)
	}
	// Mnemonic + operand may include spaces (e.g. `LDA #$42` already
	// became two tokens above when we filtered hex bytes correctly).
	// Concatenate everything up to the first "A:" or "X:" register tag.
	mnStart := i
	for i < len(fields) && !strings.HasPrefix(fields[i], "A:") {
		i++
	}
	f.Mnemonic = strings.Join(fields[mnStart:i], " ")

	// Remaining fields are register tags. Parse each "X:VAL".
	for ; i < len(fields); i++ {
		tag := fields[i]
		colon := strings.IndexByte(tag, ':')
		if colon < 0 {
			continue
		}
		key, val := tag[:colon], tag[colon+1:]
		switch key {
		case "A":
			n, _ := strconv.ParseUint(val, 16, 8)
			f.A = byte(n)
		case "X":
			n, _ := strconv.ParseUint(val, 16, 8)
			f.X = byte(n)
		case "Y":
			n, _ := strconv.ParseUint(val, 16, 8)
			f.Y = byte(n)
		case "P":
			n, _ := strconv.ParseUint(val, 16, 8)
			f.P = byte(n)
		case "SP":
			n, _ := strconv.ParseUint(val, 16, 8)
			f.SP = byte(n)
		case "CYC":
			n, _ := strconv.ParseUint(val, 10, 64)
			f.Cycles = n
		}
	}
	return f, nil
}

func isHexByte(s string) bool {
	if len(s) != 2 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// splitInterruptLine extracts the kind (NMI / IRQ) from
//
//	"---- NMI -> $FFFA (PC=$XXXX P=PP SP=SS CYC:N)"
func splitInterruptLine(line string) (kind, rest string) {
	// Drop the "---- " prefix and read until "->".
	s := strings.TrimPrefix(line, "----")
	s = strings.TrimSpace(s)
	if idx := strings.Index(s, "->"); idx > 0 {
		return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+2:])
	}
	return s, ""
}
