package cpu

// Bus abstracts memory access. 64KB flat space by default.
type Bus interface {
	Read(addr uint16) byte
	Write(addr uint16, v byte)
}

// RAM is a simple 64KB flat memory with an optional page-level
// copy-on-write shadow. When the shadow is enabled, every Write captures
// the affected 256-byte page's pre-write contents on first touch within
// the current epoch. Reverse-step snapshots use that delta to undo a
// step without copying the full 64 KiB.
type RAM struct {
	Data   [0x10000]byte
	shadow map[byte][256]byte // nil = shadow tracking disabled
	// frozen is the debugger "freeze" set (issue #422): writes to these
	// addresses are suppressed so the value holds. nil/empty by default —
	// the Write fast path is a single len check when nothing is frozen.
	frozen map[uint16]struct{}
}

func NewRAM() *RAM { return &RAM{} }

func (r *RAM) Read(addr uint16) byte { return r.Data[addr] }

func (r *RAM) Write(addr uint16, v byte) {
	if len(r.frozen) != 0 {
		if _, ok := r.frozen[addr]; ok {
			return // frozen: suppress the write (debugger freeze)
		}
	}
	if r.shadow != nil {
		page := byte(addr >> 8)
		if _, ok := r.shadow[page]; !ok {
			var img [256]byte
			base := int(page) << 8
			copy(img[:], r.Data[base:base+256])
			r.shadow[page] = img
		}
	}
	r.Data[addr] = v
}

// Load copies bytes into RAM at addr. Bypasses the shadow on purpose —
// ROM-load happens before any rewind epoch exists and shouldn't pollute
// it.
func (r *RAM) Load(addr uint16, data []byte) {
	for i, b := range data {
		r.Data[int(addr)+i] = b
	}
}

// Freeze locks a CPU-bus address to value: it sets the byte and then
// suppresses all subsequent CPU writes to it, so the value holds across
// frames (debugger freeze / cheats, issue #422). Re-freezing updates the
// held value. The set sits directly in Data (no shadow epoch) since a freeze
// is a debugger action, not a program write. Opt-in: with nothing frozen the
// Write hot path is a single len check.
func (r *RAM) Freeze(addr uint16, value byte) {
	if r.frozen == nil {
		r.frozen = make(map[uint16]struct{})
	}
	r.Data[addr] = value
	r.frozen[addr] = struct{}{}
}

// Unfreeze removes an address from the freeze set; writes resume.
func (r *RAM) Unfreeze(addr uint16) {
	delete(r.frozen, addr)
}

// Frozen reports whether addr is currently frozen.
func (r *RAM) Frozen(addr uint16) bool {
	_, ok := r.frozen[addr]
	return ok
}

// FrozenAddrs returns the currently frozen addresses (unordered).
func (r *RAM) FrozenAddrs() []uint16 {
	out := make([]uint16, 0, len(r.frozen))
	for a := range r.frozen {
		out = append(out, a)
	}
	return out
}

// EnableShadow turns on the page-level write barrier. Idempotent.
// Called by surfaces that intend to take rewind snapshots (TUI + DAP);
// leaving it off keeps Write a single bounds-checked store for tests
// and headless harnesses that don't care about reverse-step.
func (r *RAM) EnableShadow() {
	if r.shadow == nil {
		r.shadow = make(map[byte][256]byte)
	}
}

// ShadowEnabled reports whether the page-level write barrier is active.
func (r *RAM) ShadowEnabled() bool { return r.shadow != nil }

// TakeShadow returns the accumulated before-image pages since the last
// reset and starts a fresh epoch. Returns nil if shadow tracking is
// off. Caller owns the returned map.
func (r *RAM) TakeShadow() map[byte][256]byte {
	if r.shadow == nil {
		return nil
	}
	out := r.shadow
	r.shadow = make(map[byte][256]byte)
	return out
}

// ResetShadow clears the current epoch without returning its contents.
// Use after a manual write or Restore that shouldn't be folded into the
// next snapshot's delta.
func (r *RAM) ResetShadow() {
	if r.shadow != nil {
		r.shadow = make(map[byte][256]byte)
	}
}

// SaveFullState returns a copy of the full 64 KiB Data backing store
// for the nessy save-state system (#266). The cost is small enough
// that we don't bother range-restricting to the NES's 2 KiB internal
// RAM — saving the whole address space stays correct for hosts that
// directly poke higher pages.
func (r *RAM) SaveFullState() []byte {
	out := make([]byte, len(r.Data))
	copy(out, r.Data[:])
	return out
}

// LoadFullState overwrites Data from s. Lengths must match the
// 64 KiB backing store; mismatched input is rejected so a malformed
// save can't half-write memory.
func (r *RAM) LoadFullState(s []byte) error {
	if len(s) != len(r.Data) {
		return errBadStateSize
	}
	copy(r.Data[:], s)
	r.ResetShadow()
	return nil
}
