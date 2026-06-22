package cpu

import "fmt"

// Peripheral is a memory-mapped device that claims an inclusive [lo, hi]
// region of the 64KB address space. Reads and writes to that region are
// routed to the peripheral instead of the underlying Bus.
//
// Range() must be stable for the lifetime of the peripheral. Read/Write are
// called by the CPU through MMIO and may have side effects (e.g. a keyboard
// status register that clears the "data ready" bit on data read).
type Peripheral interface {
	Range() (lo, hi uint16)
	Read(addr uint16) byte
	Write(addr uint16, v byte)
}

// MMIO is a Bus that dispatches reads and writes to registered peripherals
// before falling back to an underlying Bus (typically RAM).
//
// Lookup is a linear scan: the number of peripherals in a typical 6502
// system is small (single digits), and a scan stays branch-predictor-friendly
// in the common no-MMIO case where the slice is empty. If a system grows
// past ~16 peripherals, switch to a 256-entry page table keyed on addr>>8.
type MMIO struct {
	Inner       Bus
	peripherals []Peripheral
	// frozen is the debugger "freeze" set (issue #463): CPU writes to these
	// addresses are suppressed so the value holds across frames — extends the
	// RAM-only freeze (#422) to peripheral- and cart-mapped addresses, since a
	// CPU write to a peripheral never reaches RAM. nil/empty by default — the
	// Write fast path is a single len check when nothing is frozen.
	frozen map[uint16]struct{}
}

func NewMMIO(inner Bus) *MMIO { return &MMIO{Inner: inner} }

// Register adds a peripheral. Returns an error if its range overlaps an
// already-registered peripheral. Order of registration does not matter
// because overlap is forbidden.
func (m *MMIO) Register(p Peripheral) error {
	lo, hi := p.Range()
	if lo > hi {
		return fmt.Errorf("peripheral range invalid: lo=%04X > hi=%04X", lo, hi)
	}
	for _, q := range m.peripherals {
		qlo, qhi := q.Range()
		if lo <= qhi && qlo <= hi {
			return fmt.Errorf("peripheral range %04X-%04X overlaps existing %04X-%04X", lo, hi, qlo, qhi)
		}
	}
	m.peripherals = append(m.peripherals, p)
	return nil
}

// Peripherals returns the currently registered peripherals in registration
// order. The returned slice aliases internal storage; callers must not
// mutate it.
func (m *MMIO) Peripherals() []Peripheral { return m.peripherals }

func (m *MMIO) Read(addr uint16) byte {
	for _, p := range m.peripherals {
		lo, hi := p.Range()
		if addr >= lo && addr <= hi {
			return p.Read(addr)
		}
	}
	return m.Inner.Read(addr)
}

func (m *MMIO) Write(addr uint16, v byte) {
	if len(m.frozen) != 0 {
		if _, ok := m.frozen[addr]; ok {
			return // frozen: suppress the write (debugger freeze, issue #463)
		}
	}
	m.dispatchWrite(addr, v)
}

// dispatchWrite routes a write to the owning peripheral or to Inner, without
// the freeze guard — shared by Write (post-guard) and Freeze (the value must
// land even though the address is about to be frozen).
func (m *MMIO) dispatchWrite(addr uint16, v byte) {
	for _, p := range m.peripherals {
		lo, hi := p.Range()
		if addr >= lo && addr <= hi {
			p.Write(addr, v)
			return
		}
	}
	m.Inner.Write(addr, v)
}

// Freeze locks a bus address to value: it writes the value through (to the
// owning peripheral or Inner) and then suppresses all subsequent CPU writes,
// so the value holds across frames (debugger freeze / cheats, issue #463 —
// extends RAM.Freeze #422 to MMIO/cart addresses). Re-freezing updates the
// held value. Opt-in: with nothing frozen the Write hot path is a single len
// check.
func (m *MMIO) Freeze(addr uint16, value byte) {
	m.dispatchWrite(addr, value)
	if m.frozen == nil {
		m.frozen = make(map[uint16]struct{})
	}
	m.frozen[addr] = struct{}{}
}

// Unfreeze removes an address from the freeze set; writes resume.
func (m *MMIO) Unfreeze(addr uint16) { delete(m.frozen, addr) }

// Frozen reports whether addr is currently frozen.
func (m *MMIO) Frozen(addr uint16) bool {
	_, ok := m.frozen[addr]
	return ok
}

// FrozenAddrs returns the currently frozen addresses (unordered).
func (m *MMIO) FrozenAddrs() []uint16 {
	out := make([]uint16, 0, len(m.frozen))
	for a := range m.frozen {
		out = append(out, a)
	}
	return out
}

// Tick fans out per-instruction cycle deltas to every registered
// peripheral that implements Ticker. Also forwards to Inner if it
// implements Ticker (e.g. a host wrapper around MMIO that wants its
// own bookkeeping). Satisfies the Ticker contract from ticker.go.
func (m *MMIO) Tick(cycles int) {
	for _, p := range m.peripherals {
		if t, ok := p.(Ticker); ok {
			t.Tick(cycles)
		}
	}
	if t, ok := m.Inner.(Ticker); ok {
		t.Tick(cycles)
	}
}

// Peeker is the optional "side-effect-free read" interface a
// peripheral may implement so memory inspectors (DAP `readMemory`,
// the TUI memory panel) can sample its state without driving the
// register's normal Read side effects (e.g. PPU's $2002 clears
// vblank, joypad's $4016 shifts the register, keyboard's $F004
// clears the data-ready flag).
//
// Peripherals where Read is already side-effect-free should still
// implement Peek as `return p.Read(addr)` so memory inspection
// works consistently. Peripherals that DON'T implement Peeker fall
// back to MMIO.Inner.Read at the requested address — typically
// zero, which is the "no observable state" answer for an MMIO
// inspector.
type Peeker interface {
	Peek(addr uint16) byte
}

// Peek is the side-effect-free counterpart to Read. Used by memory
// inspectors. Falls back to MMIO.Inner.Read for peripherals that
// don't implement Peeker — that's better than calling Read and
// triggering side effects the inspector shouldn't.
func (m *MMIO) Peek(addr uint16) byte {
	for _, p := range m.peripherals {
		lo, hi := p.Range()
		if addr >= lo && addr <= hi {
			if pk, ok := p.(Peeker); ok {
				return pk.Peek(addr)
			}
			// Peripheral exists at this address but doesn't expose a
			// side-effect-free read. Returning Inner.Read is the
			// conservative choice — it's whatever the underlying bus
			// has there (typically zero for unmapped peripheral
			// addresses), not the live device state.
			return m.Inner.Read(addr)
		}
	}
	return m.Inner.Read(addr)
}
