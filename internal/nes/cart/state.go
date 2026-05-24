package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// CartState is the discriminated-union save-state for any mapper.
// Exactly one of the per-mapper fields is non-nil on a populated
// state; Kind names the mapper so the loader can route the right
// branch. The cart's PRG bytes are NOT persisted — they come from
// the ROM file at load time. CHR is persisted only for CHR-RAM carts
// (CHR-ROM is immutable + part of the ROM).
type CartState struct {
	Kind  string // "NROM" | "UxROM" | "CNROM" | "MMC1" | "MMC3"
	NROM  *NROMState
	UxROM *UxROMState
	CNROM *CNROMState
	MMC1  *MMC1State
	MMC3  *MMC3State
}

// NROMState — only CHR-RAM (when present) is mutable.
type NROMState struct {
	CHRRAM []byte // nil if CHR-ROM
}

// UxROMState — bank reg + CHR-RAM.
type UxROMState struct {
	PrgBank byte
	CHRRAM  []byte
}

// CNROMState — CHR bank reg + CHR-RAM (rare).
type CNROMState struct {
	ChrBank byte
	CHRRAM  []byte
}

// MMC1State — full register file + PRG-RAM + CHR-RAM (when applicable).
type MMC1State struct {
	Shift, WriteCnt    byte
	Control            byte
	ChrBank0, ChrBank1 byte
	PrgBank            byte
	Mirroring          nes.Mirroring
	PRGRAM             [0x2000]byte
	CHRRAM             []byte // nil if CHR-ROM
}

// MMC3State — bank registers + IRQ counter + PRG-RAM + CHR-RAM (rare).
type MMC3State struct {
	BankSelect byte
	BankRegs   [8]byte
	MirrorH    bool
	FourScreen bool
	IRQLatch   byte
	IRQCounter byte
	IRQReload  bool
	IRQEnabled bool
	IRQPending bool
	PrevA12    bool
	PRGRAM     [0x2000]byte
	CHRRAM     []byte
}

// SaveCart routes through the concrete type to capture mapper-
// specific state. Unknown mappers error.
func SaveCart(c Cartridge) (CartState, error) {
	switch m := c.(type) {
	case *NROM:
		return CartState{Kind: "NROM", NROM: m.saveState()}, nil
	case *UxROM:
		return CartState{Kind: "UxROM", UxROM: m.saveState()}, nil
	case *CNROM:
		return CartState{Kind: "CNROM", CNROM: m.saveState()}, nil
	case *MMC1:
		return CartState{Kind: "MMC1", MMC1: m.saveState()}, nil
	case *MMC3:
		return CartState{Kind: "MMC3", MMC3: m.saveState()}, nil
	default:
		return CartState{}, fmt.Errorf("cart: unsupported type for save-state %T", c)
	}
}

// LoadCart applies s onto the cart c. Cart and state Kind must
// match — loading an MMC1 state into an NROM cart errors instead of
// silently dropping fields.
func LoadCart(c Cartridge, s CartState) error {
	switch m := c.(type) {
	case *NROM:
		if s.Kind != "NROM" || s.NROM == nil {
			return fmt.Errorf("cart: state kind %q doesn't match NROM cart", s.Kind)
		}
		return m.loadState(*s.NROM)
	case *UxROM:
		if s.Kind != "UxROM" || s.UxROM == nil {
			return fmt.Errorf("cart: state kind %q doesn't match UxROM cart", s.Kind)
		}
		return m.loadState(*s.UxROM)
	case *CNROM:
		if s.Kind != "CNROM" || s.CNROM == nil {
			return fmt.Errorf("cart: state kind %q doesn't match CNROM cart", s.Kind)
		}
		return m.loadState(*s.CNROM)
	case *MMC1:
		if s.Kind != "MMC1" || s.MMC1 == nil {
			return fmt.Errorf("cart: state kind %q doesn't match MMC1 cart", s.Kind)
		}
		return m.loadState(*s.MMC1)
	case *MMC3:
		if s.Kind != "MMC3" || s.MMC3 == nil {
			return fmt.Errorf("cart: state kind %q doesn't match MMC3 cart", s.Kind)
		}
		return m.loadState(*s.MMC3)
	default:
		return fmt.Errorf("cart: unsupported type for load-state %T", c)
	}
}

// --- NROM ---

func (c *NROM) saveState() *NROMState {
	s := &NROMState{}
	if c.chrIsRAM {
		s.CHRRAM = append(s.CHRRAM, c.chr...)
	}
	return s
}

func (c *NROM) loadState(s NROMState) error {
	if c.chrIsRAM {
		if len(s.CHRRAM) != len(c.chr) {
			return fmt.Errorf("nrom: CHR-RAM length mismatch (have %d, got %d)", len(c.chr), len(s.CHRRAM))
		}
		copy(c.chr, s.CHRRAM)
	}
	return nil
}

// --- UxROM ---

func (c *UxROM) saveState() *UxROMState {
	s := &UxROMState{PrgBank: c.prgBank}
	s.CHRRAM = append(s.CHRRAM, c.chr...)
	return s
}

func (c *UxROM) loadState(s UxROMState) error {
	c.prgBank = s.PrgBank
	if len(s.CHRRAM) != len(c.chr) {
		return fmt.Errorf("uxrom: CHR-RAM length mismatch (have %d, got %d)", len(c.chr), len(s.CHRRAM))
	}
	copy(c.chr, s.CHRRAM)
	return nil
}

// --- CNROM ---

func (c *CNROM) saveState() *CNROMState {
	s := &CNROMState{ChrBank: c.chrBank}
	if c.chrIsRAM {
		s.CHRRAM = append(s.CHRRAM, c.chr...)
	}
	return s
}

func (c *CNROM) loadState(s CNROMState) error {
	c.chrBank = s.ChrBank
	if c.chrIsRAM {
		if len(s.CHRRAM) != len(c.chr) {
			return fmt.Errorf("cnrom: CHR-RAM length mismatch (have %d, got %d)", len(c.chr), len(s.CHRRAM))
		}
		copy(c.chr, s.CHRRAM)
	}
	return nil
}

// --- MMC1 ---

func (c *MMC1) saveState() *MMC1State {
	s := &MMC1State{
		Shift: c.shift, WriteCnt: c.writeCnt,
		Control:  c.control,
		ChrBank0: c.chrBank0, ChrBank1: c.chrBank1,
		PrgBank:   c.prgBank,
		Mirroring: c.mirroring,
		PRGRAM:    c.prgRAM,
	}
	if c.chrIsRAM {
		s.CHRRAM = append(s.CHRRAM, c.chr...)
	}
	return s
}

func (c *MMC1) loadState(s MMC1State) error {
	c.shift = s.Shift
	c.writeCnt = s.WriteCnt
	c.control = s.Control
	c.chrBank0 = s.ChrBank0
	c.chrBank1 = s.ChrBank1
	c.prgBank = s.PrgBank
	c.mirroring = s.Mirroring
	c.prgRAM = s.PRGRAM
	if c.chrIsRAM {
		if len(s.CHRRAM) != len(c.chr) {
			return fmt.Errorf("mmc1: CHR-RAM length mismatch (have %d, got %d)", len(c.chr), len(s.CHRRAM))
		}
		copy(c.chr, s.CHRRAM)
	}
	return nil
}

// --- MMC3 ---

func (c *MMC3) saveState() *MMC3State {
	s := &MMC3State{
		BankSelect: c.bankSelect,
		BankRegs:   c.bankRegs,
		MirrorH:    c.mirrorH,
		FourScreen: c.fourScreen,
		IRQLatch:   c.irqLatch,
		IRQCounter: c.irqCounter,
		IRQReload:  c.irqReload,
		IRQEnabled: c.irqEnabled,
		IRQPending: c.irqPending,
		PrevA12:    c.prevA12,
		PRGRAM:     c.prgRAM,
	}
	if c.chrIsRAM {
		s.CHRRAM = append(s.CHRRAM, c.chr...)
	}
	return s
}

func (c *MMC3) loadState(s MMC3State) error {
	c.bankSelect = s.BankSelect
	c.bankRegs = s.BankRegs
	c.mirrorH = s.MirrorH
	c.fourScreen = s.FourScreen
	c.irqLatch = s.IRQLatch
	c.irqCounter = s.IRQCounter
	c.irqReload = s.IRQReload
	c.irqEnabled = s.IRQEnabled
	c.irqPending = s.IRQPending
	c.prevA12 = s.PrevA12
	c.prgRAM = s.PRGRAM
	if c.chrIsRAM {
		if len(s.CHRRAM) != len(c.chr) {
			return fmt.Errorf("mmc3: CHR-RAM length mismatch (have %d, got %d)", len(c.chr), len(s.CHRRAM))
		}
		copy(c.chr, s.CHRRAM)
	}
	return nil
}
