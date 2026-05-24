// Package nes hosts NES-specific (Ricoh 2A03 + PPU + APU + cartridge)
// types for the nessy emulator. The cpu / peripheral / dap packages
// at chippy's root are reused unchanged; nes/ adds the platform
// hardware on top.
package nes

import (
	"errors"
	"fmt"
	"io"
)

// Magic is the iNES header signature "NES\x1A".
var iNESMagic = [4]byte{'N', 'E', 'S', 0x1A}

const (
	headerLen   = 16
	trainerLen  = 512
	prgBankSize = 16 * 1024
	chrBankSize = 8 * 1024
)

// Mirroring is the nametable-mirroring scheme a cartridge advertises.
// PPU rendering depends on it for correct background addressing.
type Mirroring uint8

const (
	MirrorHorizontal Mirroring = iota
	MirrorVertical
	MirrorFourScreen
	// MirrorSingleLower / MirrorSingleUpper are the two
	// "single-screen" modes MMC1 (and a few other mappers) flip in
	// at runtime via control-register writes. The iNES header
	// never advertises these directly — only mappers set them.
	MirrorSingleLower
	MirrorSingleUpper
)

func (m Mirroring) String() string {
	switch m {
	case MirrorVertical:
		return "vertical"
	case MirrorFourScreen:
		return "four-screen"
	case MirrorSingleLower:
		return "single-lower"
	case MirrorSingleUpper:
		return "single-upper"
	default:
		return "horizontal"
	}
}

// ROM is a parsed iNES (or NES 2.0) cartridge image. It owns the
// PRG-ROM + CHR-ROM byte slices and the header flags a mapper
// constructor needs.
type ROM struct {
	Mapper    uint16 // 12-bit mapper number per NES 2.0 (low 8 bits = iNES 1.0)
	SubMapper uint8  // NES 2.0 sub-mapper (0 when header is iNES 1.0 or sub-mapper unused)
	Mirroring Mirroring
	Battery   bool // PRG-RAM battery-backed
	Trainer   []byte
	PRG       []byte // n × 16 KiB
	CHR       []byte // n × 8 KiB, or nil/empty for CHR-RAM carts
	NES2      bool   // true when the header advertises NES 2.0

	// NES 2.0 extensions. Zero values for iNES 1.0 ROMs.
	PRGRAMSize int      // $6000-$7FFF SRAM size in bytes
	EEPROMSize int      // EEPROM (battery-backed PRG-RAM) size in bytes
	CHRRAMSize int      // CHR-RAM size in bytes (0 = use CHR-ROM)
	TVSystem   TVSystem // NTSC / PAL / Dual
}

// TVSystem names the cart's intended TV-system per NES 2.0 flag12.
// chippy currently runs the NTSC timing diagram regardless; the hint
// is recorded for future PAL / Dendy support.
type TVSystem uint8

const (
	TVNTSC TVSystem = iota
	TVPAL
	TVDual // NTSC + PAL (both expected to work)
	TVDendy
)

func (t TVSystem) String() string {
	switch t {
	case TVPAL:
		return "PAL"
	case TVDual:
		return "dual"
	case TVDendy:
		return "Dendy"
	default:
		return "NTSC"
	}
}

// Errors returned from Parse.
var (
	ErrBadMagic    = errors.New("ines: bad magic (not an iNES file)")
	ErrTruncated   = errors.New("ines: file truncated mid-bank")
	ErrZeroPRG     = errors.New("ines: header claims 0 PRG-ROM banks")
	ErrInvalidSize = errors.New("ines: PRG/CHR sizes don't match file length")
)

// Parse reads an iNES (or NES 2.0) file from r and returns the
// constructed ROM. The whole file is buffered in memory; NES carts
// are at most a few MB so this is acceptable.
func Parse(r io.Reader) (*ROM, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("ines: read: %w", err)
	}
	return ParseBytes(data)
}

// ParseBytes is the in-memory shortcut Parse delegates to. Useful for
// embedded test fixtures.
func ParseBytes(data []byte) (*ROM, error) {
	if len(data) < headerLen {
		return nil, ErrTruncated
	}
	if [4]byte{data[0], data[1], data[2], data[3]} != iNESMagic {
		return nil, ErrBadMagic
	}
	prgBanks := int(data[4])
	chrBanks := int(data[5])
	flag6 := data[6]
	flag7 := data[7]

	if prgBanks == 0 {
		return nil, ErrZeroPRG
	}

	rom := &ROM{
		Mapper:  uint16((flag7 & 0xF0) | (flag6 >> 4)),
		Battery: flag6&0x02 != 0,
	}
	switch {
	case flag6&0x08 != 0:
		rom.Mirroring = MirrorFourScreen
	case flag6&0x01 != 0:
		rom.Mirroring = MirrorVertical
	default:
		rom.Mirroring = MirrorHorizontal
	}
	// NES 2.0 detection: bits 2-3 of flag7 == 0b10 ("NES 2 ID").
	rom.NES2 = (flag7 & 0x0C) == 0x08
	if rom.NES2 {
		parseNES2Extensions(data, rom, &prgBanks, &chrBanks)
	}

	off := headerLen
	if flag6&0x04 != 0 { // trainer present
		if len(data) < off+trainerLen {
			return nil, ErrTruncated
		}
		rom.Trainer = data[off : off+trainerLen]
		off += trainerLen
	}

	prgSize := prgBanks * prgBankSize
	if len(data) < off+prgSize {
		return nil, ErrTruncated
	}
	rom.PRG = data[off : off+prgSize]
	off += prgSize

	if chrBanks > 0 {
		chrSize := chrBanks * chrBankSize
		if len(data) < off+chrSize {
			return nil, ErrTruncated
		}
		rom.CHR = data[off : off+chrSize]
		off += chrSize
	}
	// Anything past `off` is allowed (PlayChoice-10 INST-ROM trailer,
	// title text, NES 2.0 footer) but ignored in v0.1.
	if off > len(data) {
		return nil, ErrInvalidSize
	}
	return rom, nil
}

// parseNES2Extensions reads the NES 2.0-specific bytes (flag8 +
// flag9 + flag10 + flag12) and writes the derived fields into rom.
// Also extends PRG / CHR bank counts when the v2.0 high nibbles
// are non-zero. Per the spec:
//
//	flag8 bits 0-3 → high nibble of 12-bit mapper number.
//	flag8 bits 4-7 → 4-bit sub-mapper.
//	flag9 bits 0-3 → high nibble of PRG bank count (combined with
//	                 flag4 to form a 12-bit value; if the high
//	                 nibble is $F the value is computed as an
//	                 exponent + multiplier per the spec).
//	flag9 bits 4-7 → same for CHR.
//	flag10 bits 0-3 → PRG-RAM size (2 << v) bytes when v != 0.
//	flag10 bits 4-7 → EEPROM size (2 << v) bytes when v != 0.
//	flag11 — same shape for CHR-RAM + CHR-NVRAM.
//	flag12 bits 0-1 → TV system (0 = NTSC, 1 = PAL, 2 = dual,
//	                  3 = Dendy).
//
// chippy currently consumes the mapper / sub-mapper / TV-system
// hints; PRG/CHR-RAM/EEPROM sizes are recorded but not yet acted
// on (cart constructors still use fixed 8 KiB PRG-RAM).
func parseNES2Extensions(data []byte, rom *ROM, prgBanks, chrBanks *int) {
	flag8 := data[8]
	flag9 := data[9]
	flag10 := data[10]
	flag11 := data[11]
	flag12 := data[12]

	// Mapper high nibble + sub-mapper.
	rom.Mapper |= uint16(flag8&0x0F) << 8
	rom.SubMapper = (flag8 & 0xF0) >> 4

	// Extended PRG / CHR bank counts. NES 2.0 uses flag9's nibbles
	// as the high 4 bits of a 12-bit bank count; if the nibble is
	// $F, the corresponding flag4/flag5 byte is interpreted as
	// (M × 2^E) with M = bits 0-1 + 1 and E = bits 2-7 (exponential
	// encoding). chippy implements the linear path + the exponent
	// fallback.
	*prgBanks = extendedBankCount(flag9&0x0F, data[4])
	*chrBanks = extendedBankCount((flag9&0xF0)>>4, data[5])

	// PRG-RAM + EEPROM. Both nibbles store (2 << v) bytes when
	// non-zero. v = 0 means absent (size 0).
	if v := flag10 & 0x0F; v != 0 {
		rom.PRGRAMSize = 64 << v
	}
	if v := (flag10 & 0xF0) >> 4; v != 0 {
		rom.EEPROMSize = 64 << v
	}
	// CHR-RAM + CHR-NVRAM (flag11). We only track CHR-RAM size.
	if v := flag11 & 0x0F; v != 0 {
		rom.CHRRAMSize = 64 << v
	}

	switch flag12 & 0x03 {
	case 1:
		rom.TVSystem = TVPAL
	case 2:
		rom.TVSystem = TVDual
	case 3:
		rom.TVSystem = TVDendy
	default:
		rom.TVSystem = TVNTSC
	}
}

// extendedBankCount returns the bank count given the v2.0 high
// nibble + the v1.0 low byte. Implements both linear (high < $F)
// and exponential (high == $F) encodings per the NES 2.0 spec.
func extendedBankCount(high4, lo byte) int {
	if high4 == 0x0F {
		// Exponent / multiplier: M = bits 0-1 + 1, E = bits 2-7.
		// Size = M × 2^E. lo encodes both.
		exp := int((lo >> 2) & 0x3F)
		mul := int((lo&0x03)<<1) | 1 // M ∈ {1, 3, 5, 7}
		return mul << exp
	}
	return int(high4)<<8 | int(lo)
}
