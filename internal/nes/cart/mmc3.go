package cart

import (
	"fmt"

	"github.com/nkane/chippy/internal/nes"
)

// MMC3 is mapper 4 — Nintendo's late-NES workhorse. Powers SMB3,
// Mega Man 3-6, Kirby's Adventure, Crystalis, Battletoads, and
// most late-catalog titles. The headline feature is the scanline
// IRQ that fires on A12 rising edges (PPU pattern-table fetch
// boundary) so games can split the screen into a status bar +
// scrolling playfield using sprite/BG fetch timing rather than
// sprite-0 hit polling.
//
// Register file (write-only at $8000-$FFFF; even/odd address bit
// 0 picks which sub-register):
//
//	$8000 even — Bank select:
//	    bits 0-2: which R0..R7 the next $8001 write lands in.
//	    bit 6:    PRG bank mode.
//	    bit 7:    CHR A12 invert (CHR bank mode).
//	$8001 odd  — Bank data: writes to whichever R0..R7 $8000
//	    last selected.
//	$A000 even — Mirroring (bit 0: 0 = vertical, 1 = horizontal).
//	    Ignored on 4-screen carts.
//	$A001 odd  — PRG-RAM protect (bits 6 + 7). Cosmetic on most
//	    modern emulators; we accept the writes silently.
//	$C000 even — IRQ counter reload value.
//	$C001 odd  — IRQ counter reload (latches; the actual counter
//	    reloads on the NEXT A12 rising edge).
//	$E000 even — IRQ enable off + clear any pending IRQ.
//	$E001 odd  — IRQ enable on.
//
// PRG bank modes (control bit 6):
//
//	mode 0: $8000 = R6, $A000 = R7, $C000 = fixed(N-2), $E000 = fixed(N-1)
//	mode 1: $8000 = fixed(N-2), $A000 = R7, $C000 = R6, $E000 = fixed(N-1)
//
// CHR bank modes (control bit 7):
//
//	mode 0: $0000-$0FFF = R0|R1 (two 2 KiB); $1000-$1FFF = R2..R5 (four 1 KiB)
//	mode 1: $0000-$0FFF = R2..R5 (four 1 KiB); $1000-$1FFF = R0|R1 (two 2 KiB)
//
// R0 + R1 are 2 KiB banks (low bit of the value ignored when used).
// R2..R5 are 1 KiB banks.
//
// IRQ counter timing: decremented on each A12 rising edge during
// rendering. On counter underflow (when armed) the IRQ line goes
// to the CPU via the named source "mmc3". $E000 acks.
type MMC3 struct {
	prg      []byte
	chr      []byte
	chrIsRAM bool
	prgRAM   [0x2000]byte
	battery  bool

	bankSelect byte // last $8000 even write
	bankRegs   [8]byte
	mirrorH    bool // $A000 bit 0
	fourScreen bool
	irqLatch   byte
	irqCounter byte
	irqReload  bool
	irqEnabled bool
	irqPending bool
	prevA12    bool

	irqSink IRQSink
}

// IRQSink is the cart's view of the CPU's named-source IRQ
// surface from #247. *cpu.CPU satisfies it via AssertIRQSource /
// ClearIRQSource. cmd/nessy/wiring.go calls SetIRQSink after both
// peripherals exist.
type IRQSink interface {
	AssertIRQSource(src string)
	ClearIRQSource(src string)
}

const mmc3IRQSource = "mmc3"

// NewMMC3 constructs an MMC3 cart from a parsed iNES ROM. PRG
// must be a non-zero multiple of 8 KiB (MMC3's PRG bank size);
// CHR is either 0 (CHR-RAM, unusual for MMC3) or a non-zero
// multiple of 1 KiB.
func NewMMC3(rom *nes.ROM) (*MMC3, error) {
	if len(rom.PRG) == 0 || len(rom.PRG)%(8*1024) != 0 {
		return nil, fmt.Errorf("mmc3: PRG must be a non-zero multiple of 8 KiB; got %d bytes", len(rom.PRG))
	}
	c := &MMC3{
		prg:        rom.PRG,
		battery:    rom.Battery,
		fourScreen: rom.Mirroring == nes.MirrorFourScreen,
	}
	switch {
	case len(rom.CHR) == 0:
		c.chr = make([]byte, 8*1024)
		c.chrIsRAM = true
	case len(rom.CHR)%(1*1024) == 0:
		c.chr = rom.CHR
	default:
		return nil, fmt.Errorf("mmc3: CHR must be 0 or a multiple of 1 KiB; got %d bytes", len(rom.CHR))
	}
	// Power-on mirroring: ROM-header-derived for the first frame
	// before the game writes $A000.
	c.mirrorH = rom.Mirroring == nes.MirrorHorizontal
	return c, nil
}

// SetIRQSink wires the CPU's IRQ-source surface. May be nil for
// headless tests — IRQ flag still tracks; just nothing on the CPU
// line.
func (c *MMC3) SetIRQSink(s IRQSink) { c.irqSink = s }

// CPURead serves $6000-$FFFF.
func (c *MMC3) CPURead(addr uint16) byte {
	switch {
	case addr < 0x6000:
		return 0
	case addr < 0x8000:
		return c.prgRAM[addr-0x6000]
	}
	return c.prg[c.prgOffset(addr)]
}

// CPUWrite handles PRG-RAM at $6000-$7FFF + register writes at
// $8000-$FFFF. Even/odd address bit 0 picks the sub-register
// within each register window.
func (c *MMC3) CPUWrite(addr uint16, v byte) {
	switch {
	case addr < 0x6000:
		return
	case addr < 0x8000:
		c.prgRAM[addr-0x6000] = v
		return
	}
	window := addr & 0xE001
	switch window {
	case 0x8000:
		c.bankSelect = v
	case 0x8001:
		c.bankRegs[c.bankSelect&0x07] = v
	case 0xA000:
		c.mirrorH = v&0x01 != 0
	case 0xA001:
		// PRG-RAM protect — accepted, not enforced.
	case 0xC000:
		c.irqLatch = v
	case 0xC001:
		c.irqReload = true
	case 0xE000:
		c.irqEnabled = false
		c.irqPending = false
		if c.irqSink != nil {
			c.irqSink.ClearIRQSource(mmc3IRQSource)
		}
	case 0xE001:
		c.irqEnabled = true
	}
}

// prgOffset computes the byte offset into c.prg for a CPU address
// in $8000-$FFFF based on the active PRG bank mode + bank
// registers.
func (c *MMC3) prgOffset(addr uint16) int {
	bankSize := 8 * 1024
	totalBanks := len(c.prg) / bankSize
	last := totalBanks - 1
	mode := c.bankSelect & 0x40
	var bank int
	region := (addr >> 13) & 0x03 // 0..3 for $8000/$A000/$C000/$E000
	switch region {
	case 0:
		if mode == 0 {
			bank = int(c.bankRegs[6] & 0x3F)
		} else {
			bank = last - 1
		}
	case 1:
		bank = int(c.bankRegs[7] & 0x3F)
	case 2:
		if mode == 0 {
			bank = last - 1
		} else {
			bank = int(c.bankRegs[6] & 0x3F)
		}
	case 3:
		bank = last
	}
	bank %= totalBanks
	off := int(addr&0x1FFF) + bank*bankSize
	return off % len(c.prg)
}

// PPURead returns CHR data + clocks the A12 IRQ counter on rising
// edges. A12 = bit 12 of the PPU bus address; PPU fetches BG
// patterns from $0000-$0FFF (A12 low) and sprite patterns from
// $1000-$1FFF (A12 high), so during render the line toggles at
// predictable scanline boundaries.
func (c *MMC3) PPURead(addr uint16) byte {
	c.clockA12(addr)
	if addr >= 0x2000 {
		return 0
	}
	return c.chr[c.chrOffset(addr)]
}

// PPUWrite is effective for CHR-RAM carts only. Still clocks A12.
func (c *MMC3) PPUWrite(addr uint16, v byte) {
	c.clockA12(addr)
	if addr >= 0x2000 || !c.chrIsRAM {
		return
	}
	c.chr[c.chrOffset(addr)] = v
}

// chrOffset computes the byte offset into c.chr for a PPU address
// in $0000-$1FFF based on the active CHR bank mode + bank
// registers.
func (c *MMC3) chrOffset(addr uint16) int {
	totalBytes := len(c.chr)
	mode := c.bankSelect & 0x80
	// Effective region in 1 KiB slots: 0..7.
	slot := int(addr>>10) & 0x07
	if mode != 0 {
		// CHR A12 invert: swap low/high halves.
		slot ^= 0x04
	}
	var bank int
	switch slot {
	case 0:
		bank = int(c.bankRegs[0] & 0xFE)
	case 1:
		bank = int(c.bankRegs[0] | 0x01)
	case 2:
		bank = int(c.bankRegs[1] & 0xFE)
	case 3:
		bank = int(c.bankRegs[1] | 0x01)
	case 4:
		bank = int(c.bankRegs[2])
	case 5:
		bank = int(c.bankRegs[3])
	case 6:
		bank = int(c.bankRegs[4])
	case 7:
		bank = int(c.bankRegs[5])
	}
	off := bank*1024 + int(addr&0x03FF)
	return off % totalBytes
}

// clockA12 decrements the IRQ counter on every rising edge of bit
// 12 of the PPU address bus. A12 must stay low for at least one
// "filter cycle" before the next rising edge counts — without the
// debounce, fine-X scrolling can produce spurious back-to-back
// triggers.
//
// Real silicon's filter is ~16 CPU cycles; we approximate by
// gating on whether A12 was low last time PPURead/PPUWrite ran.
// This is the "Sharp" MMC3 variant (the more common one); the
// "ALT" / NEC revision differs slightly in reload semantics and
// is a v0.5+ follow-up.
func (c *MMC3) clockA12(addr uint16) {
	a12 := addr&0x1000 != 0
	rising := a12 && !c.prevA12
	c.prevA12 = a12
	if !rising {
		return
	}
	if c.irqCounter == 0 || c.irqReload {
		c.irqCounter = c.irqLatch
		c.irqReload = false
	} else {
		c.irqCounter--
	}
	if c.irqCounter == 0 && c.irqEnabled {
		c.irqPending = true
		if c.irqSink != nil {
			c.irqSink.AssertIRQSource(mmc3IRQSource)
		}
	}
}

// Mirroring derives from the runtime $A000 bit 0; 4-screen carts
// keep the iNES MirrorFourScreen value (ignored by $A000 writes).
func (c *MMC3) Mirroring() nes.Mirroring {
	if c.fourScreen {
		return nes.MirrorFourScreen
	}
	if c.mirrorH {
		return nes.MirrorHorizontal
	}
	return nes.MirrorVertical
}

// BatteryBacked + PRGRAM match the MMC1 surface so cmd/nessy's
// save/restore handles MMC3 carts identically.
func (c *MMC3) BatteryBacked() bool { return c.battery }
func (c *MMC3) PRGRAM() []byte      { return c.prgRAM[:] }
