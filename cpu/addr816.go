package cpu

// 65816 addressing-mode engine (#456 chunk 2). Each resolver reads its operand
// bytes from PBR:PC, computes the 24-bit effective data address, and returns
// the operand/pointer "overhead" cycle subtotal (everything except the data
// transfer itself and the indexed page-cross penalty, both of which the opcode
// kernel adds once it knows the access width and direction). The direct-page
// penalty (+1 when DL≠0) is baked into the overhead here since DL is known at
// resolve time.
//
// Address-space wrap rules: direct-page and stack-relative effective addresses
// live in bank 0 and a 16-bit value wraps within bank 0 (ea816.bank0). All
// other modes form a full 24-bit address whose high byte carries into the next
// bank. The emulation-mode DL=0 quirk (direct-page indexing and pointer fetches
// wrap within page $00xx, the 6502 zero-page behavior) is honored in dpBase /
// readDPWord / readDPLong.

// ea816 is a resolved 65816 effective address.
type ea816 struct {
	addr  uint32 // 24-bit effective data address
	bank0 bool   // a 16-bit value at addr wraps within bank 0 (dp / stack-rel)
	idx   bool   // indexed mode subject to the page-cross / extra-index cycle
	cross bool   // the index add crossed a page boundary (read penalty)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// eaInc returns the address of the high byte of a multi-byte value at a.
func (c *CPU) eaInc(a uint32, bank0 bool) uint32 {
	if bank0 {
		return uint32(uint16(a) + 1)
	}
	return (a + 1) & 0xFFFFFF
}

func (c *CPU) readEA(e ea816, wide bool) uint16 {
	lo := uint16(c.read24(e.addr))
	if !wide {
		return lo
	}
	return lo | uint16(c.read24(c.eaInc(e.addr, e.bank0)))<<8
}

func (c *CPU) writeEA(e ea816, wide bool, v uint16) {
	c.write24(e.addr, byte(v))
	if wide {
		c.write24(c.eaInc(e.addr, e.bank0), byte(v>>8))
	}
}

func (c *CPU) dlPenalty() int {
	if c.D&0xFF != 0 {
		return 1
	}
	return 0
}

// dpBase is the bank-0 direct-page effective address D + dp + idx, honoring the
// emulation DL=0 page-wrap quirk.
func (c *CPU) dpBase(dp byte, idx uint16) uint32 {
	if c.E && c.D&0xFF == 0 {
		return uint32(c.D) | uint32((uint16(dp)+idx)&0xFF)
	}
	return uint32((uint16(c.D) + uint16(dp) + idx) & 0xFFFF)
}

// readDPWord reads a 16-bit pointer from direct page at D+dp+idx. The emulation
// DL=0 page-wrap (dpBase) applies only to the base offset; the pointer's bytes
// then increment with a flat 16-bit (bank-0) address, which can leave page $xx00.
func (c *CPU) readDPWord(dp byte, idx uint16) uint16 {
	base := c.dpBase(dp, idx)
	lo := uint16(c.read24(base))
	hi := uint16(c.read24(uint32(uint16(base) + 1)))
	return lo | hi<<8
}

// readDPLong reads a 24-bit pointer from direct page at D+dp (flat 16-bit
// increment from the base — the [dp] form does not page-wrap even in emulation).
func (c *CPU) readDPLong(dp byte) uint32 {
	base := c.dpBase(dp, 0)
	b0 := uint32(c.read24(base))
	b1 := uint32(c.read24(uint32(uint16(base) + 1)))
	b2 := uint32(c.read24(uint32(uint16(base) + 2)))
	return b0 | b1<<8 | b2<<16
}

// readDPLongWrap reads a 24-bit pointer for the [dp],Y form. Unlike [dp], each
// of its three bytes honors the emulation DL=0 page-wrap (the pointer stays
// within page DH:xx); in native mode it is a flat bank-0 increment.
func (c *CPU) readDPLongWrap(dp byte) uint32 {
	b0 := uint32(c.read24(c.dpOff(dp, 0)))
	b1 := uint32(c.read24(c.dpOff(dp, 1)))
	b2 := uint32(c.read24(c.dpOff(dp, 2)))
	return b0 | b1<<8 | b2<<16
}

// dpOff is the bank-0 address of byte offset off from direct-page base D+dp,
// honoring the emulation DL=0 page-wrap.
func (c *CPU) dpOff(dp byte, off uint16) uint32 {
	if c.E && c.D&0xFF == 0 {
		return uint32(c.D) | uint32((uint16(dp)+off)&0xFF)
	}
	return uint32((uint16(c.D) + uint16(dp) + off) & 0xFFFF)
}

func (c *CPU) fetch16() uint16 {
	lo := uint16(c.fetch816())
	return lo | uint16(c.fetch816())<<8
}

func (c *CPU) fetch24() uint32 {
	lo := uint32(c.fetch816())
	mid := uint32(c.fetch816())
	hi := uint32(c.fetch816())
	return lo | mid<<8 | hi<<16
}

// --- addressing-mode resolvers: return (effective address, overhead cycles) ---

func (c *CPU) amDP() (ea816, int) {
	dp := c.fetch816()
	return ea816{addr: c.dpBase(dp, 0), bank0: true}, 2 + c.dlPenalty()
}

func (c *CPU) amDPIdx(idx uint16) (ea816, int) {
	dp := c.fetch816()
	return ea816{addr: c.dpBase(dp, idx), bank0: true}, 3 + c.dlPenalty()
}

func (c *CPU) amAbs() (ea816, int) {
	a := c.fetch16()
	return ea816{addr: uint32(c.DBR)<<16 | uint32(a)}, 3
}

func (c *CPU) amAbsIdx(idx uint16) (ea816, int) {
	base := c.fetch16()
	full := uint32(c.DBR)<<16 | uint32(base)
	ea := (full + uint32(idx)) & 0xFFFFFF
	cross := (base & 0xFF00) != (uint16(base+idx) & 0xFF00)
	return ea816{addr: ea, idx: true, cross: cross}, 3
}

func (c *CPU) amLong() (ea816, int) {
	return ea816{addr: c.fetch24()}, 4
}

func (c *CPU) amLongX() (ea816, int) {
	a := c.fetch24()
	return ea816{addr: (a + uint32(c.Xidx())) & 0xFFFFFF}, 4
}

func (c *CPU) amIndDP() (ea816, int) {
	dp := c.fetch816()
	ptr := c.readDPWord(dp, 0)
	return ea816{addr: uint32(c.DBR)<<16 | uint32(ptr)}, 4 + c.dlPenalty()
}

func (c *CPU) amIndDPY() (ea816, int) {
	dp := c.fetch816()
	ptr := c.readDPWord(dp, 0)
	full := uint32(c.DBR)<<16 | uint32(ptr)
	idx := c.Yidx()
	ea := (full + uint32(idx)) & 0xFFFFFF
	cross := (ptr & 0xFF00) != (uint16(ptr+idx) & 0xFF00)
	return ea816{addr: ea, idx: true, cross: cross}, 4 + c.dlPenalty()
}

func (c *CPU) amIndDPX() (ea816, int) {
	dp := c.fetch816()
	ptr := c.readDPWord(dp, c.Xidx())
	return ea816{addr: uint32(c.DBR)<<16 | uint32(ptr)}, 5 + c.dlPenalty()
}

func (c *CPU) amIndLongDP() (ea816, int) {
	dp := c.fetch816()
	return ea816{addr: c.readDPLong(dp)}, 5 + c.dlPenalty()
}

func (c *CPU) amIndLongDPY() (ea816, int) {
	dp := c.fetch816()
	ptr := c.readDPLongWrap(dp)
	return ea816{addr: (ptr + uint32(c.Yidx())) & 0xFFFFFF}, 5 + c.dlPenalty()
}

func (c *CPU) amStackRel() (ea816, int) {
	sr := c.fetch816()
	return ea816{addr: uint32((c.SP16() + uint16(sr)) & 0xFFFF), bank0: true}, 3
}

func (c *CPU) amStackRelIndY() (ea816, int) {
	sr := c.fetch816()
	p := (c.SP16() + uint16(sr)) & 0xFFFF
	lo := uint16(c.read24(uint32(p)))
	hi := uint16(c.read24(uint32((p + 1) & 0xFFFF)))
	ptr := lo | hi<<8
	full := uint32(c.DBR)<<16 | uint32(ptr)
	return ea816{addr: (full + uint32(c.Yidx())) & 0xFFFFFF}, 6
}

// --- cross/extra-cycle penalties ---

// crossLoad is the indexed read penalty: +1 when the index is 16-bit (always)
// or the add crossed a page boundary.
func (c *CPU) crossLoad(e ea816) int {
	if e.idx && (c.xWide() || e.cross) {
		return 1
	}
	return 0
}

// crossStore is the indexed write/RMW penalty: the extra index cycle is always
// taken regardless of crossing.
func crossStore(e ea816) int {
	if e.idx {
		return 1
	}
	return 0
}
