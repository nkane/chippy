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

// cpuCyclesPerFrame mirrors cmd/nessy's constant — ~29830 cycles per
// 60 Hz frame at the 1.789773 MHz NTSC clock.
const cpuCyclesPerFrame = 29830

// bus is the minimal NES wiring the recorder drives. Duplicated from
// cmd/nessy/wiring.go + cmd/nessy-wasm/main.go because the three
// package-main binaries can't share the type without a refactor
// (tracked separately — factor buildBus into internal/nesbus).
type bus struct {
	cpu *cpu.CPU
	ppu *ppu.PPU
	joy *joypad.Port
	apu *apu.APU
}

type cartPeripheral struct{ cart cart.Cartridge }

func (w *cartPeripheral) Range() (uint16, uint16)   { return 0x4020, 0xFFFF }
func (w *cartPeripheral) Read(addr uint16) byte     { return w.cart.CPURead(addr) }
func (w *cartPeripheral) Write(addr uint16, v byte) { w.cart.CPUWrite(addr, v) }
func (w *cartPeripheral) Peek(addr uint16) byte     { return w.cart.CPURead(addr) }
func (w *cartPeripheral) Tick(cycles int) {
	if t, ok := w.cart.(cpu.Ticker); ok {
		t.Tick(cycles)
	}
}

func buildBus(rom *nes.ROM) (*bus, error) {
	c, err := cart.Open(rom)
	if err != nil {
		return nil, err
	}
	ram := cpu.NewRAM()
	mmio := cpu.NewMMIO(ram)
	if err := mmio.Register(&cartPeripheral{cart: c}); err != nil {
		return nil, err
	}
	jp := joypad.New()
	if err := mmio.Register(jp); err != nil {
		return nil, err
	}
	ap := apu.New()
	if err := mmio.Register(ap); err != nil {
		return nil, err
	}
	if err := mmio.Register(apu.NewStatus(ap)); err != nil {
		return nil, err
	}
	jp.AttachFrameCounter(ap)

	processor := cpu.NewVariant(mmio, cpu.VariantNES)
	ap.SetIRQSink(processor)
	ap.SetDMCBus(mmio, processor)
	if m, ok := c.(interface{ SetIRQSink(cart.IRQSink) }); ok {
		m.SetIRQSink(processor)
	}
	if m, ok := c.(interface{ SetAudioSink(cart.Sunsoft5BSink) }); ok {
		s5b := apu.NewSunsoft5B()
		ap.SetSunsoft5B(s5b)
		m.SetAudioSink(s5b)
	}
	if m, ok := c.(interface{ SetAudioSink(cart.VRC6AudioSink) }); ok {
		v6 := apu.NewVRC6Audio()
		ap.SetVRC6Audio(v6)
		m.SetAudioSink(v6)
	}
	if m, ok := c.(interface{ SetAudioSink(cart.VRC7AudioSink) }); ok {
		v7 := apu.NewVRC7Audio()
		ap.SetVRC7Audio(v7)
		m.SetAudioSink(v7)
	}

	pp := ppu.New(c, processor)
	if err := mmio.Register(pp); err != nil {
		return nil, err
	}
	if err := mmio.Register(dma.New(mmio, pp, processor)); err != nil {
		return nil, err
	}
	processor.Reset()
	return &bus{cpu: processor, ppu: pp, joy: jp, apu: ap}, nil
}

// stepFrame advances the CPU by one frame's worth of cycles.
func (b *bus) stepFrame() {
	target := b.cpu.Cycles + cpuCyclesPerFrame
	for b.cpu.Cycles < target && !b.cpu.Halted {
		b.cpu.Step()
	}
}
