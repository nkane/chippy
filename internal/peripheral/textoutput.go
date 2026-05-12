// Package peripheral provides memory-mapped I/O devices that plug into
// cpu.MMIO. Each peripheral claims a small region of the 6502 address
// space; reads and writes to that region are routed to the device.
package peripheral

// DefaultTextOutputCap caps the live TextOutput buffer at 64 KiB by
// default. A long-running program that loops writes to $F001 would
// otherwise grow the slice without bound (and inflate every reverse-step
// snapshot along with it). 64 KiB is enough to hold a full screen of
// output many times over while keeping memory predictable.
const DefaultTextOutputCap = 64 * 1024

// TextOutput is an Apple-1-style write-only console at a single address
// (conventionally $F001). Each byte written is appended to an internal
// buffer that the TUI can render.
//
// The Apple-1 monitor uses bit 7 as a "ready" sentinel and ASCII in the
// lower bits; for chippy we accept the raw byte and let the renderer
// decide. CR (0x0D) is translated to LF (0x0A) so naive monitor programs
// produce the line breaks a Unix terminal expects.
//
// The buffer is bounded by Cap (zero means unbounded, mostly useful for
// tests). When a write would overflow, the oldest quarter of the buffer
// is dropped — amortizing the shift cost across many writes while
// keeping reverse-step snapshots small.
type TextOutput struct {
	Addr uint16
	Cap  int // 0 = unbounded
	buf  []byte
}

// NewTextOutput creates a TextOutput peripheral at addr with the default
// 64 KiB buffer cap. Use NewTextOutputWithCap to override.
func NewTextOutput(addr uint16) *TextOutput {
	return &TextOutput{Addr: addr, Cap: DefaultTextOutputCap}
}

// NewTextOutputWithCap creates a TextOutput at addr with an explicit
// buffer cap. cap <= 0 disables bounding.
func NewTextOutputWithCap(addr uint16, cap int) *TextOutput {
	if cap < 0 {
		cap = 0
	}
	return &TextOutput{Addr: addr, Cap: cap}
}

func (t *TextOutput) Range() (uint16, uint16) { return t.Addr, t.Addr }

// Read returns 0; the Apple-1 PIA never wired a read path for $D012.
// Returning a stable zero matches that convention and keeps the bus
// well-defined for code that probes the region.
func (t *TextOutput) Read(addr uint16) byte { return 0 }

func (t *TextOutput) Write(addr uint16, v byte) {
	if addr != t.Addr {
		return
	}
	// Apple-1 ROMs send CR; translate to LF for terminal-friendly output.
	if v == 0x0D {
		v = 0x0A
	}
	// Apple-1 conventions: bit 7 set on the high ASCII. Strip it so the
	// buffer carries plain 7-bit text.
	if t.Cap > 0 && len(t.buf) >= t.Cap {
		// Drop the oldest quarter so the next batch of writes is O(1)
		// again instead of triggering this shift every byte.
		drop := t.Cap / 4
		if drop < 1 {
			drop = 1
		}
		t.buf = append(t.buf[:0], t.buf[drop:]...)
	}
	t.buf = append(t.buf, v&0x7F)
}

// Bytes returns a copy of the buffered output.
func (t *TextOutput) Bytes() []byte {
	out := make([]byte, len(t.buf))
	copy(out, t.buf)
	return out
}

// String returns the buffered output as a string.
func (t *TextOutput) String() string { return string(t.buf) }

// Len returns the number of bytes buffered.
func (t *TextOutput) Len() int { return len(t.buf) }

// Reset clears the buffer.
func (t *TextOutput) Reset() { t.buf = t.buf[:0] }

// Snapshot returns a deep copy of the buffer for reverse-step restoration.
// Implements peripheral.Snapshotable so the CPU snapshot ring can round-trip
// peripheral state alongside CPU + RAM.
func (t *TextOutput) Snapshot() []byte {
	out := make([]byte, len(t.buf))
	copy(out, t.buf)
	return out
}

// Restore replaces the buffer with the supplied bytes. The slice is copied
// so the caller can reuse its backing array.
func (t *TextOutput) Restore(state []byte) {
	t.buf = append(t.buf[:0], state...)
}
