package peripheral

// ACIA emulates the 6551 serial UART, the console interface on many 6502 SBCs
// (notably the Ben Eater 6502 kit). It claims four consecutive registers from
// Base:
//
//	Base+0  DATA    read = next RX byte (clears RDRF); write = TX byte
//	Base+1  STATUS  read = status flags (clears the IRQ flag); write = reset
//	Base+2  COMMAND r/w — DTR, RX/TX interrupt control, parity
//	Base+3  CONTROL r/w — baud / word-length / stop bits (cosmetic here)
//
// Emulation model: transmission is instantaneous — a written byte lands in the
// TX sink immediately and TDRE (transmit-register-empty) reads as always ready,
// so polled-TX monitors never block. Received bytes come from a host queue
// (Receive), mirroring KeyboardInput. When receiver interrupts are enabled
// (COMMAND bit 1 clear) an arriving byte asserts the CPU IRQ line via the
// IRQSink; reading STATUS or DATA clears it. Transmit interrupts are not
// generated (TDRE is always set, so TX is polled) — documented, not a bug.
//
// Concurrency: like KeyboardInput, Receive and the bus Read/Write are serialized
// by the Bubble Tea goroutine ordering; do not Receive from another goroutine
// without a mutex.
type ACIA struct {
	Base uint16

	irq   IRQSink // CPU interrupt line; nil = no interrupts
	txCap int
	tx    []byte // transmitted bytes (the "wire")
	rxq   []byte // pending received bytes

	command byte // Base+2
	control byte // Base+3
	irqFlag bool // STATUS bit 7 — this ACIA raised the current IRQ
}

// IRQSink is the CPU interrupt-line surface a peripheral drives. *cpu.CPU
// satisfies it (AssertIRQSource / ClearIRQSource). nil means the peripheral
// runs without interrupts.
type IRQSink interface {
	AssertIRQSource(src string)
	ClearIRQSource(src string)
}

// 6551 STATUS register bits.
const (
	aciaStatusRDRF = 1 << 3 // receive data register full
	aciaStatusTDRE = 1 << 4 // transmit data register empty
	aciaStatusIRQ  = 1 << 7 // interrupt occurred (cleared on STATUS read)
)

// aciaIRQSource is the named IRQ source the ACIA holds on the wired-OR line.
const aciaIRQSource = "acia"

// NewACIA creates a 6551 at Base with the default 64 KiB TX-sink cap.
func NewACIA(base uint16) *ACIA {
	return &ACIA{Base: base, txCap: DefaultTextOutputCap}
}

// SetIRQ wires the CPU interrupt line so the ACIA can raise receiver
// interrupts. Pass the *cpu.CPU. nil disables interrupts.
func (a *ACIA) SetIRQ(irq IRQSink) { a.irq = irq }

func (a *ACIA) Range() (uint16, uint16) { return a.Base, a.Base + 3 }

// rxIRQEnabled reports whether receiver interrupts are on: 6551 COMMAND bit 1
// (IRD) clear enables them.
func (a *ACIA) rxIRQEnabled() bool { return a.command&0x02 == 0 }

func (a *ACIA) status() byte {
	s := byte(aciaStatusTDRE) // transmit always ready (instantaneous TX)
	if len(a.rxq) > 0 {
		s |= aciaStatusRDRF
	}
	if a.irqFlag {
		s |= aciaStatusIRQ
	}
	return s
}

// clearIRQ drops the IRQ flag and releases the CPU line.
func (a *ACIA) clearIRQ() {
	if a.irqFlag {
		a.irqFlag = false
		if a.irq != nil {
			a.irq.ClearIRQSource(aciaIRQSource)
		}
	}
}

func (a *ACIA) Read(addr uint16) byte {
	switch addr - a.Base {
	case 0: // DATA — dequeue one RX byte
		var b byte
		if len(a.rxq) > 0 {
			b = a.rxq[0]
			a.rxq = a.rxq[1:]
		}
		if len(a.rxq) == 0 {
			a.clearIRQ()
		}
		return b
	case 1: // STATUS — reading clears the IRQ flag
		s := a.status()
		a.clearIRQ()
		return s
	case 2:
		return a.command
	case 3:
		return a.control
	}
	return 0
}

// Peek is the side-effect-free debug read (peripheral.Peeker): no dequeue, no
// IRQ clear.
func (a *ACIA) Peek(addr uint16) byte {
	switch addr - a.Base {
	case 0:
		if len(a.rxq) > 0 {
			return a.rxq[0]
		}
		return 0
	case 1:
		return a.status()
	case 2:
		return a.command
	case 3:
		return a.control
	}
	return 0
}

func (a *ACIA) Write(addr uint16, v byte) {
	switch addr - a.Base {
	case 0: // DATA — transmit
		if a.txCap > 0 && len(a.tx) >= a.txCap {
			drop := a.txCap / 4
			if drop < 1 {
				drop = 1
			}
			a.tx = append(a.tx[:0], a.tx[drop:]...)
		}
		a.tx = append(a.tx, v)
	case 1: // STATUS write = programmed reset (clears IRQ enables + IRQ)
		a.command &^= 0x1F // datasheet: reset clears the low command bits
		a.clearIRQ()
	case 2:
		a.command = v
		// Enabling RX IRQ with a byte already waiting raises it immediately.
		if a.rxIRQEnabled() && len(a.rxq) > 0 {
			a.raiseRX()
		} else if !a.rxIRQEnabled() {
			a.clearIRQ()
		}
	case 3:
		a.control = v
	}
}

// Receive queues a byte from the host side (e.g. a keystroke) as the next byte
// the 6502 will read from DATA, raising a receiver interrupt if enabled.
func (a *ACIA) Receive(b byte) {
	a.rxq = append(a.rxq, b)
	if a.rxIRQEnabled() {
		a.raiseRX()
	}
}

func (a *ACIA) raiseRX() {
	a.irqFlag = true
	if a.irq != nil {
		a.irq.AssertIRQSource(aciaIRQSource)
	}
}

// TxBytes returns a copy of everything the program has transmitted.
func (a *ACIA) TxBytes() []byte {
	out := make([]byte, len(a.tx))
	copy(out, a.tx)
	return out
}

// TxString returns transmitted output as a string.
func (a *ACIA) TxString() string { return string(a.tx) }

// ResetTx clears the transmit sink.
func (a *ACIA) ResetTx() { a.tx = a.tx[:0] }

// RxPending reports how many received bytes are queued (tests / status line).
func (a *ACIA) RxPending() int { return len(a.rxq) }

// Snapshot packs the ACIA's state for the reverse-step ring (Snapshotable):
// command, control, irqFlag, then a length-prefixed RX queue, then the TX sink.
func (a *ACIA) Snapshot() []byte {
	irq := byte(0)
	if a.irqFlag {
		irq = 1
	}
	out := []byte{a.command, a.control, irq, byte(len(a.rxq) >> 8), byte(len(a.rxq))}
	out = append(out, a.rxq...)
	out = append(out, a.tx...)
	return out
}

// Restore unpacks Snapshot's form. Out-of-shape input clears state.
func (a *ACIA) Restore(state []byte) {
	if len(state) < 5 {
		a.command, a.control, a.irqFlag, a.rxq, a.tx = 0, 0, false, nil, nil
		return
	}
	a.command = state[0]
	a.control = state[1]
	a.irqFlag = state[2] != 0
	n := int(state[3])<<8 | int(state[4])
	rest := state[5:]
	if n > len(rest) {
		n = len(rest)
	}
	a.rxq = append([]byte(nil), rest[:n]...)
	a.tx = append([]byte(nil), rest[n:]...)
}
