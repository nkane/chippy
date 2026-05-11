package peripheral

// KeyboardInput is an Apple-1-style keyboard at two consecutive addresses:
//
//	dataAddr   ($F004): read = last pressed key (bit 7 forced high to match
//	                    Apple-1 monitor expectations); also clears the
//	                    "ready" bit on the status register.
//	statusAddr ($F005): read = 0x80 when a key is pending, 0x00 otherwise.
//
// Writes to either register are ignored — keyboards are input-only.
//
// Concurrency: in chippy the TUI's Update handler and the CPU's Step both
// run in the Bubble Tea goroutine, so Push and Read are already serialized
// by that ordering. Do not call Push from a separate goroutine without
// adding a mutex.
type KeyboardInput struct {
	DataAddr   uint16
	StatusAddr uint16

	data  byte
	ready bool
}

// NewKeyboardInput creates a peripheral at (dataAddr, statusAddr). The
// canonical Apple-1 mapping is (0xF004, 0xF005); chippy follows that.
func NewKeyboardInput(dataAddr, statusAddr uint16) *KeyboardInput {
	return &KeyboardInput{DataAddr: dataAddr, StatusAddr: statusAddr}
}

func (k *KeyboardInput) Range() (uint16, uint16) {
	if k.DataAddr <= k.StatusAddr {
		return k.DataAddr, k.StatusAddr
	}
	return k.StatusAddr, k.DataAddr
}

// Push records a byte as the next key the 6502 will see. If a previous
// keystroke has not yet been read, it is overwritten — matching the
// Apple-1's single-byte latched PIA register.
func (k *KeyboardInput) Push(b byte) {
	k.data = b
	k.ready = true
}

func (k *KeyboardInput) Read(addr uint16) byte {
	switch addr {
	case k.DataAddr:
		k.ready = false
		return k.data | 0x80 // Apple-1 monitor expects bit 7 set
	case k.StatusAddr:
		if k.ready {
			return 0x80
		}
		return 0x00
	}
	return 0
}

func (k *KeyboardInput) Write(addr uint16, v byte) {
	// Apple-1 keyboard PIA had no writable host-side state; ignore.
}

// Ready reports whether a keystroke is pending. Exposed for tests and the
// TUI status line.
func (k *KeyboardInput) Ready() bool { return k.ready }
