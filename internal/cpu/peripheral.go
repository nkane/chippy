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
	for _, p := range m.peripherals {
		lo, hi := p.Range()
		if addr >= lo && addr <= hi {
			p.Write(addr, v)
			return
		}
	}
	m.Inner.Write(addr, v)
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
