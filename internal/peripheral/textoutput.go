// Package peripheral provides memory-mapped I/O devices that plug into
// cpu.MMIO. Each peripheral claims a small region of the 6502 address
// space; reads and writes to that region are routed to the device.
package peripheral

// TextOutput is an Apple-1-style write-only console at a single address
// (conventionally $F001). Each byte written is appended to an internal
// buffer that the TUI can render.
//
// The Apple-1 monitor uses bit 7 as a "ready" sentinel and ASCII in the
// lower bits; for chippy we accept the raw byte and let the renderer
// decide. CR (0x0D) is translated to LF (0x0A) so naive monitor programs
// produce the line breaks a Unix terminal expects.
type TextOutput struct {
	Addr uint16
	buf  []byte
}

// NewTextOutput creates a TextOutput peripheral at addr.
func NewTextOutput(addr uint16) *TextOutput { return &TextOutput{Addr: addr} }

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
