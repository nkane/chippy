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

// ROM is a parsed iNES cartridge image. It owns the PRG-ROM + CHR-ROM
// byte slices and the header flags a mapper constructor needs.
type ROM struct {
	Mapper    uint8
	Mirroring Mirroring
	Battery   bool // PRG-RAM battery-backed
	Trainer   []byte
	PRG       []byte // n × 16 KiB
	CHR       []byte // n × 8 KiB, or nil/empty for CHR-RAM carts
	NES2      bool   // true when the header advertises NES 2.0 (extensions parsed best-effort)
	// nes2Submapper / nes2PrgRam / etc — future fields. v0.1 ignores
	// them and treats the file as iNES 1.0 for mapper construction.
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
		Mapper:  (flag7 & 0xF0) | (flag6 >> 4),
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
