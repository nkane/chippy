package nes

import (
	"bytes"
	"errors"
	"testing"
)

// buildiNES constructs a synthetic iNES file with the given header
// flags and bank counts. PRG / CHR bytes are filler patterns so tests
// can verify the right region got mapped.
func buildiNES(prgBanks, chrBanks int, flag6, flag7 byte, trainer bool) []byte {
	var b bytes.Buffer
	b.Write([]byte{'N', 'E', 'S', 0x1A})
	b.WriteByte(byte(prgBanks))
	b.WriteByte(byte(chrBanks))
	if trainer {
		flag6 |= 0x04
	}
	b.WriteByte(flag6)
	b.WriteByte(flag7)
	// Padding to 16-byte header.
	for i := 8; i < 16; i++ {
		b.WriteByte(0)
	}
	if trainer {
		for i := 0; i < trainerLen; i++ {
			b.WriteByte(0xAA)
		}
	}
	for i := 0; i < prgBanks*prgBankSize; i++ {
		b.WriteByte(0xBB)
	}
	for i := 0; i < chrBanks*chrBankSize; i++ {
		b.WriteByte(0xCC)
	}
	return b.Bytes()
}

// Happy path: 1 PRG, 1 CHR, NROM, horizontal mirroring, no battery.
func TestParse_NROMBasic(t *testing.T) {
	data := buildiNES(1, 1, 0x00, 0x00, false)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rom.Mapper != 0 {
		t.Errorf("mapper = %d; want 0 (NROM)", rom.Mapper)
	}
	if rom.Mirroring != MirrorHorizontal {
		t.Errorf("mirroring = %s; want horizontal", rom.Mirroring)
	}
	if rom.Battery {
		t.Errorf("battery should be false")
	}
	if rom.Trainer != nil {
		t.Errorf("trainer should be nil")
	}
	if len(rom.PRG) != prgBankSize {
		t.Errorf("PRG size = %d; want %d", len(rom.PRG), prgBankSize)
	}
	if len(rom.CHR) != chrBankSize {
		t.Errorf("CHR size = %d; want %d", len(rom.CHR), chrBankSize)
	}
	if rom.PRG[0] != 0xBB || rom.CHR[0] != 0xCC {
		t.Errorf("bank fillers misaligned: PRG[0]=$%02X CHR[0]=$%02X", rom.PRG[0], rom.CHR[0])
	}
}

// Mapper number splits across flag6 high nibble and flag7 high
// nibble. Verify the assembly: mapper 4 (MMC3) sets flag6=0x40, flag7=0x00.
// Mapper 7 (AOROM) sets flag7=0x70, flag6=0x00 (with flag7's NES2 bits
// kept clear).
func TestParse_MapperByteAssembly(t *testing.T) {
	cases := []struct {
		name         string
		flag6, flag7 byte
		want         uint8
	}{
		{"mapper 0 (NROM)", 0x00, 0x00, 0},
		{"mapper 1 (MMC1)", 0x10, 0x00, 1},
		{"mapper 4 (MMC3)", 0x40, 0x00, 4},
		{"mapper 7 (AOROM)", 0x70, 0x00, 7},
		{"mapper 66 (GxROM)", 0x20, 0x40, 0x42},
	}
	for _, c := range cases {
		data := buildiNES(1, 1, c.flag6, c.flag7, false)
		rom, err := ParseBytes(data)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if rom.Mapper != c.want {
			t.Errorf("%s: mapper = %d; want %d", c.name, rom.Mapper, c.want)
		}
	}
}

func TestParse_MirroringFlags(t *testing.T) {
	cases := []struct {
		flag6 byte
		want  Mirroring
	}{
		{0x00, MirrorHorizontal},
		{0x01, MirrorVertical},
		{0x08, MirrorFourScreen}, // bit 3 overrides bit 0
		{0x09, MirrorFourScreen}, // four-screen wins
	}
	for _, c := range cases {
		data := buildiNES(1, 0, c.flag6, 0x00, false)
		rom, err := ParseBytes(data)
		if err != nil {
			t.Fatalf("flag6=$%02X: %v", c.flag6, err)
		}
		if rom.Mirroring != c.want {
			t.Errorf("flag6=$%02X mirroring=%s; want %s", c.flag6, rom.Mirroring, c.want)
		}
	}
}

// Battery-backed flag pulled from flag6 bit 1.
func TestParse_BatteryFlag(t *testing.T) {
	data := buildiNES(1, 0, 0x02, 0x00, false)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !rom.Battery {
		t.Errorf("battery should be true")
	}
}

// Trainer (512 bytes after the header, before PRG).
func TestParse_TrainerPresent(t *testing.T) {
	data := buildiNES(1, 1, 0x00, 0x00, true)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rom.Trainer) != trainerLen {
		t.Fatalf("trainer size = %d; want %d", len(rom.Trainer), trainerLen)
	}
	if rom.Trainer[0] != 0xAA {
		t.Errorf("trainer[0] = $%02X; want $AA", rom.Trainer[0])
	}
	if rom.PRG[0] != 0xBB {
		t.Errorf("trainer leaked into PRG: PRG[0]=$%02X", rom.PRG[0])
	}
}

// CHR-RAM cart: 0 CHR banks in the header. CHR slice should be nil.
func TestParse_CHRRAMCart(t *testing.T) {
	data := buildiNES(2, 0, 0x00, 0x00, false)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rom.PRG) != 2*prgBankSize {
		t.Errorf("PRG size = %d; want %d", len(rom.PRG), 2*prgBankSize)
	}
	if rom.CHR != nil {
		t.Errorf("CHR should be nil for CHR-RAM cart; got %d bytes", len(rom.CHR))
	}
}

// NES 2.0 detection: flag7 bits 2-3 == 0b10.
func TestParse_NES2Detection(t *testing.T) {
	data := buildiNES(1, 1, 0x00, 0x08, false)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !rom.NES2 {
		t.Errorf("NES 2.0 flag should be set when flag7 bits 2-3 = 0b10")
	}
}

// Malformed inputs reject with a clean error.
func TestParse_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrTruncated},
		{"too short for header", []byte{'N', 'E', 'S'}, ErrTruncated},
		{"bad magic", append([]byte{'X', 'Y', 'Z', 0x1A}, make([]byte, 12)...), ErrBadMagic},
		{"zero PRG banks", append([]byte{'N', 'E', 'S', 0x1A, 0, 0, 0, 0}, make([]byte, 8)...), ErrZeroPRG},
		{
			"truncated mid-PRG",
			func() []byte {
				d := buildiNES(2, 0, 0x00, 0x00, false)
				return d[:headerLen+prgBankSize] // only one PRG bank instead of two
			}(),
			ErrTruncated,
		},
	}
	for _, c := range cases {
		_, err := ParseBytes(c.data)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v; want %v", c.name, err, c.want)
		}
	}
}

// Parse() and ParseBytes() agree on the same byte stream.
func TestParse_ReaderMatchesByteSlice(t *testing.T) {
	data := buildiNES(1, 1, 0x01, 0x00, false)
	romA, err := ParseBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	romB, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if romA.Mapper != romB.Mapper || romA.Mirroring != romB.Mirroring ||
		len(romA.PRG) != len(romB.PRG) || len(romA.CHR) != len(romB.CHR) {
		t.Fatalf("Parse vs ParseBytes diverged")
	}
}
