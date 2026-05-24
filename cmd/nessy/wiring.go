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
	"github.com/nkane/chippy/internal/nes/apu"
	"github.com/nkane/chippy/internal/nes/cart"
	"github.com/nkane/chippy/internal/nes/dma"
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
	dma  *dma.OAMDMA
	apu  *apu.APU
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

	// APU first — joypad's $4017 forwarder needs the sink ready
	// before the CPU starts touching either peripheral. The APU
	// claims $4000-$4013 (channel registers). $4015 (status /
	// channel enables) registers separately via apu.StatusPeripheral
	// so it doesn't collide with $4014 OAMDMA. $4017 writes flow
	// in through jp.AttachFrameCounter below.
	ap := apu.New()
	if err := mmio.Register(ap); err != nil {
		return nil, err
	}
	if err := mmio.Register(apu.NewStatus(ap)); err != nil {
		return nil, err
	}
	jp.AttachFrameCounter(ap)

	processor := cpu.NewVariant(mmio, cpu.VariantNES)
	// CPU exists now — wire the APU's named-source IRQ sink so
	// frame-counter IRQ + DMC IRQ reach the processor's interrupt
	// path. Also wire the DMC's CPU-bus access + stall hook so
	// sample-byte DMA can fetch through MMIO and charge the
	// 4-cycle bus-steal stall.
	ap.SetIRQSink(processor)
	ap.SetDMCBus(mmio, processor)
	// MMC3 carts assert IRQ on the named "mmc3" source via the same
	// multi-source pump. Type-assertion through the interface so
	// non-MMC3 carts (NROM / MMC1 / UxROM / CNROM) silently skip
	// the wiring.
	if mmc3, ok := c.(interface{ SetIRQSink(cart.IRQSink) }); ok {
		mmc3.SetIRQSink(processor)
	}
	// FME-7 (mapper 69) optionally pairs with the Sunsoft 5B audio
	// expansion (#306). Construct the audio chip, hand it to the APU
	// as a mixer addend, and wire the cart's $C000/$E000 port pair to
	// forward writes through to it. Non-FME7 carts skip the wiring.
	if fme7, ok := c.(interface{ SetAudioSink(cart.Sunsoft5BSink) }); ok {
		s5b := apu.NewSunsoft5B()
		ap.SetSunsoft5B(s5b)
		fme7.SetAudioSink(s5b)
	}
	// NewVariant called Reset() before MMIO had the cart's $FFFC vector
	// visible? No — we registered the cart above, so Reset's vector
	// fetch returns the right bytes via the MMIO → cart-peripheral path.

	pp := ppu.New(c, processor)
	if err := mmio.Register(pp); err != nil {
		return nil, err
	}

	// $4014 OAMDMA. Reads bytes from the CPU bus (via MMIO) and pushes
	// them into the PPU's OAM cursor. Must register AFTER the PPU so
	// the source-byte reads for $2000-$3FFF cases land on the live
	// PPU. The 513-cycle bus-steal stall flows through cpu.Stall →
	// Step()'s drain on the next instruction boundary.
	oam := dma.New(mmio, pp, processor)
	if err := mmio.Register(oam); err != nil {
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
		dma:  oam,
		apu:  ap,
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

// Tick forwards per-instruction CPU cycle deltas to the inner cart
// when it implements cpu.Ticker. FME-7's IRQ counter (mapper 69)
// ticks at CPU rate via this hook; MMC3's A12-edge IRQ doesn't need
// it (already fires from PPU bus accesses).
func (w *cartPeripheral) Tick(cycles int) {
	if t, ok := w.cart.(cpu.Ticker); ok {
		t.Tick(cycles)
	}
}

// compile-time check.
var (
	_ cpu.Peripheral = (*cartPeripheral)(nil)
	_ cpu.Peeker     = (*cartPeripheral)(nil)
	_ cpu.Ticker     = (*cartPeripheral)(nil)
)
