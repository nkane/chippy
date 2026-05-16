// Package main implements `nessy`, the NES emulator that uses chippy's
// CPU + bus infrastructure under an Ebiten window. The Ebiten-touching
// surfaces live behind a `//go:build nessy` tag because building Ebiten
// on Linux requires X11 / GL dev headers that the default CI runners
// don't carry; users on darwin / windows can drop the tag. Wiring lives
// here, untagged, so it stays unit-testable on every platform.
package main

import (
	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/nes"
	"github.com/nkane/chippy/internal/nes/cart"
	"github.com/nkane/chippy/internal/nes/joypad"
	"github.com/nkane/chippy/internal/nes/ppu"
)

// nesBus is the assembled NES — every component the Ebiten game loop or
// the DAP server needs to touch. cart is exposed for save-state work
// (battery PRG-RAM); ram is the 2 KiB internal RAM mirrored at $0000-
// $1FFF on the CPU bus.
type nesBus struct {
	cpu  *cpu.CPU
	ppu  *ppu.PPU
	joy  *joypad.Port
	mmio *cpu.MMIO
	ram  *cpu.RAM
	cart cart.Cartridge
}

// buildNES wires the CPU, PPU, joypad port, and cart into a runnable
// NES bus. Construction order matters:
//
//  1. Open the cart so its CPU-side wrapper has something to dispatch.
//  2. Build MMIO over fresh RAM and register the cart + joypad. The
//     cart must be present BEFORE the CPU constructor runs, since
//     NewVariant calls Reset() which reads the cart's $FFFC reset
//     vector through MMIO.
//  3. Build the CPU; its Reset picks up the reset vector via cart.
//  4. Build the PPU, handing it the cart (for PPU-bus pattern access)
//     and the CPU (for the NMI line). Register the PPU on MMIO so the
//     game's $2000-$3FFF writes flow.
func buildNES(rom *nes.ROM) (*nesBus, error) {
	c, err := cart.Open(rom)
	if err != nil {
		return nil, err
	}

	ram := cpu.NewRAM()
	mmio := cpu.NewMMIO(ram)

	cartPeri := &cartPeripheral{cart: c}
	if err := mmio.Register(cartPeri); err != nil {
		return nil, err
	}
	jp := joypad.New()
	if err := mmio.Register(jp); err != nil {
		return nil, err
	}

	processor := cpu.NewVariant(mmio, cpu.VariantNES)
	// NewVariant called Reset() before MMIO had the cart's $FFFC vector
	// visible? No — we registered the cart above, so Reset's vector
	// fetch returns the right bytes via the MMIO → cart-peripheral path.

	pp := ppu.New(c, processor)
	if err := mmio.Register(pp); err != nil {
		return nil, err
	}
	// Re-run reset now that the PPU is registered. Some ROMs touch PPU
	// registers in their very first instructions, so we want those to
	// hit the PPU rather than fall through to RAM during the moments
	// between CPU construction and PPU registration.
	processor.Reset()

	return &nesBus{
		cpu:  processor,
		ppu:  pp,
		joy:  jp,
		mmio: mmio,
		ram:  ram,
		cart: c,
	}, nil
}

// cartPeripheral is the cpu.Peripheral wrapper around a cart.Cartridge,
// claiming the entire $4020-$FFFF window the cart legally responds to
// on the CPU bus. Lower addresses ($4016-$4017 joypad, $4000-$4015
// future APU) are routed to their own peripherals; $0000-$3FFF go
// through MMIO's normal fallback path (RAM mirror at $0000-$1FFF,
// PPU at $2000-$3FFF).
type cartPeripheral struct {
	cart cart.Cartridge
}

func (w *cartPeripheral) Range() (uint16, uint16) { return 0x4020, 0xFFFF }
func (w *cartPeripheral) Read(addr uint16) byte   { return w.cart.CPURead(addr) }
func (w *cartPeripheral) Write(addr uint16, v byte) {
	w.cart.CPUWrite(addr, v)
}

// Peek surfaces cart bytes without side effects. NROM's CPURead is
// already side-effect-free (just a slice index); future bank-switching
// mappers (MMC1+) should keep this contract — Peek returns whatever
// the currently-mapped bank shows.
func (w *cartPeripheral) Peek(addr uint16) byte { return w.cart.CPURead(addr) }

// compile-time check.
var (
	_ cpu.Peripheral = (*cartPeripheral)(nil)
	_ cpu.Peeker     = (*cartPeripheral)(nil)
)
