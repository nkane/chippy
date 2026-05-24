package apu

// VRC7Audio is the stub for the YM2413 (OPLL) FM-synth chip on
// Konami's VRC7 cart (mapper 85). v0.6 ships the cart side (#303)
// so Lagrange Point loads + plays its gameplay. The full FM synth
// (6 channels × 2 operators with envelope generator + KSL + vibrato
// + 15-instrument patch ROM) is its own multi-week project tracked
// under v0.7.
//
// The stub captures every register write so a future apu.OPLL can
// drop in by reading these snapshots; Output() returns 0 so the
// chip stays silent until the synth lands.
type VRC7Audio struct {
	regAddr byte
	regs    [64]byte
}

// NewVRC7Audio constructs a silent OPLL stub.
func NewVRC7Audio() *VRC7Audio { return &VRC7Audio{} }

// Write implements cart.VRC7AudioSink. $9010 latches the register
// address; $9030 writes the data.
func (v *VRC7Audio) Write(addr uint16, val byte) {
	switch addr {
	case 0x9010:
		v.regAddr = val & 0x3F
	case 0x9030:
		v.regs[v.regAddr] = val
	}
}

// Output returns 0 — the FM synth isn't implemented yet. Future
// apu.OPLL will replace this stub.
func (v *VRC7Audio) Output() int { return 0 }
