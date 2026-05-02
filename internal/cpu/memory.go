package cpu

// Bus abstracts memory access. 64KB flat space by default.
type Bus interface {
	Read(addr uint16) byte
	Write(addr uint16, v byte)
}

// RAM is a simple 64KB flat memory.
type RAM struct {
	Data [0x10000]byte
}

func NewRAM() *RAM { return &RAM{} }

func (r *RAM) Read(addr uint16) byte       { return r.Data[addr] }
func (r *RAM) Write(addr uint16, v byte)   { r.Data[addr] = v }

// Load copies bytes into RAM at addr.
func (r *RAM) Load(addr uint16, data []byte) {
	for i, b := range data {
		r.Data[int(addr)+i] = b
	}
}
