package cpu

// 65816 register model + width helpers (#456).
//
// The 65816 has a 24-bit address space and 16-bit registers whose widths are
// gated by the M (accumulator) and X (index) status bits in native mode. The
// 8-bit A/X/Y/SP fields are the low bytes; B/XH/YH/SPHi hold the high halves.
// In emulation mode (E=1) registers are 8-bit and M/X read as 1 (the 6502's
// U/B bits), so the *Wide helpers report false.

// FlagM and FlagX alias P bits 5 and 4. In native mode they are the
// accumulator-width and index-width selects (1 = 8-bit, 0 = 16-bit); in
// emulation mode the same bits are the 6502's U (unused) and B (break).
const (
	FlagM = FlagU // bit 5: native accumulator width (set = 8-bit)
	FlagX = FlagB // bit 4: native index width (set = 8-bit)
)

// mWide reports whether the accumulator is 16-bit (native mode, M clear).
func (c *CPU) mWide() bool { return !c.E && c.P&FlagM == 0 }

// xWide reports whether the index registers are 16-bit (native mode, X clear).
func (c *CPU) xWide() bool { return !c.E && c.P&FlagX == 0 }

// A16 / X16 / Y16 / SP16 read the full 16-bit registers.
func (c *CPU) A16() uint16  { return uint16(c.B)<<8 | uint16(c.A) }
func (c *CPU) X16() uint16  { return uint16(c.XH)<<8 | uint16(c.X) }
func (c *CPU) Y16() uint16  { return uint16(c.YH)<<8 | uint16(c.Y) }
func (c *CPU) SP16() uint16 { return uint16(c.SPHi)<<8 | uint16(c.SP) }

// setA16 / setX16 / setY16 / setSP16 write the full 16-bit registers.
func (c *CPU) setA16(v uint16) { c.A, c.B = byte(v), byte(v>>8) }
func (c *CPU) setX16(v uint16) { c.X, c.XH = byte(v), byte(v>>8) }
func (c *CPU) setY16(v uint16) { c.Y, c.YH = byte(v), byte(v>>8) }

// setSP16 writes the stack pointer. In emulation mode the high byte is locked
// to $01 (the 65816 forces SH=$01 while E=1), so the seeded high byte is
// ignored.
func (c *CPU) setSP16(v uint16) {
	c.SP = byte(v)
	if c.E {
		c.SPHi = 0x01
	} else {
		c.SPHi = byte(v >> 8)
	}
}

// Bus24 is the 24-bit memory interface the 65816 reads/writes through (its
// 16 MB address space, bank:offset). chippy's 8-bit cores use the 16-bit Bus;
// the 65816 variant uses a Bus24 supplied via SetBus24. The two are
// independent — the 65816 core never goes through the 16-bit Bus.
type Bus24 interface {
	Read24(addr uint32) byte
	Write24(addr uint32, v byte)
}

// SetBus24 installs the 24-bit memory the 65816 core uses. addr is masked to
// 24 bits on every access.
func (c *CPU) SetBus24(b Bus24) { c.bus24 = b }

// read24 / write24 are the 65816 core's memory primitives (24-bit, wrapping at
// 16 MB).
func (c *CPU) read24(addr uint32) byte     { return c.bus24.Read24(addr & 0xFFFFFF) }
func (c *CPU) write24(addr uint32, v byte) { c.bus24.Write24(addr&0xFFFFFF, v) }

// Xidx / Yidx return the index registers at the current index width (full
// 16-bit in native X=0, else the low byte).
func (c *CPU) Xidx() uint16 {
	if c.xWide() {
		return c.X16()
	}
	return uint16(c.X)
}
func (c *CPU) Yidx() uint16 {
	if c.xWide() {
		return c.Y16()
	}
	return uint16(c.Y)
}
