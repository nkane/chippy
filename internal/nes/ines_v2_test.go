package nes

import (
	"bytes"
	"testing"
)

// buildNES2 constructs a synthetic NES 2.0 file. flag7 always has
// the NES2 ID bits set (0b10 in bits 2-3); the rest of flag8-12 is
// caller-controlled.
func buildNES2(prgBanks, chrBanks int, flag6, flag8, flag9, flag10, flag11, flag12 byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{'N', 'E', 'S', 0x1A})
	b.WriteByte(byte(prgBanks))
	b.WriteByte(byte(chrBanks))
	b.WriteByte(flag6)
	b.WriteByte(0x08) // flag7 = NES 2.0 ID (bits 2-3 = 0b10)
	b.WriteByte(flag8)
	b.WriteByte(flag9)
	b.WriteByte(flag10)
	b.WriteByte(flag11)
	b.WriteByte(flag12)
	b.WriteByte(0) // flag13
	b.WriteByte(0) // flag14
	b.WriteByte(0) // flag15
	// PRG / CHR payload — only matters for the parse not erroring.
	for range prgBanks * prgBankSize {
		b.WriteByte(0xBB)
	}
	for range chrBanks * chrBankSize {
		b.WriteByte(0xCC)
	}
	return b.Bytes()
}

// NES 2.0 header parses + sets the NES2 flag.
func TestNES2_DetectsIDBits(t *testing.T) {
	data := buildNES2(1, 1, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if !rom.NES2 {
		t.Errorf("NES2 flag not set on v2.0 header")
	}
}

// 12-bit mapper number: low byte from flag6/7 + high nibble from
// flag8 bits 0-3.
func TestNES2_12BitMapper(t *testing.T) {
	// flag6 high nibble = 4 (low half of mapper), flag7 high nibble
	// = 5 (middle), flag8 low nibble = 7 (high nibble of 12-bit).
	// Combined mapper = (7 << 8) | (5 << 4) | 4 = $754.
	data := buildNES2(1, 1, 0x40, 0x07, 0x00, 0x00, 0x00, 0x00)
	// flag7 high nibble was overwritten by buildNES2's NES2 ID
	// byte ($08) — that means bits 4-7 of flag7 are 0. So the
	// middle nibble is 0 in this synthetic. Expected mapper:
	// (7 << 8) | (0 << 4) | 4 = $704.
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	want := uint16(0x704)
	if rom.Mapper != want {
		t.Errorf("Mapper = $%03X; want $%03X", rom.Mapper, want)
	}
}

// Sub-mapper from flag8 high nibble.
func TestNES2_SubMapper(t *testing.T) {
	data := buildNES2(1, 1, 0x00, 0x30, 0x00, 0x00, 0x00, 0x00) // sub-mapper 3
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if rom.SubMapper != 3 {
		t.Errorf("SubMapper = %d; want 3", rom.SubMapper)
	}
}

// PRG-RAM size from flag10 low nibble (size = 64 << v).
func TestNES2_PRGRAMSize(t *testing.T) {
	data := buildNES2(1, 1, 0x00, 0x00, 0x00, 0x07, 0x00, 0x00) // v=7 → 8 KiB
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if rom.PRGRAMSize != 8*1024 {
		t.Errorf("PRGRAMSize = %d; want 8192", rom.PRGRAMSize)
	}
}

// EEPROM size from flag10 high nibble.
func TestNES2_EEPROMSize(t *testing.T) {
	data := buildNES2(1, 1, 0x00, 0x00, 0x00, 0x70, 0x00, 0x00) // v=7 → 8 KiB
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if rom.EEPROMSize != 8*1024 {
		t.Errorf("EEPROMSize = %d; want 8192", rom.EEPROMSize)
	}
}

// TV system from flag12 bits 0-1.
func TestNES2_TVSystem(t *testing.T) {
	cases := []struct {
		flag12 byte
		want   TVSystem
	}{
		{0x00, TVNTSC},
		{0x01, TVPAL},
		{0x02, TVDual},
		{0x03, TVDendy},
	}
	for _, c := range cases {
		data := buildNES2(1, 1, 0x00, 0x00, 0x00, 0x00, 0x00, c.flag12)
		rom, err := ParseBytes(data)
		if err != nil {
			t.Fatalf("flag12=$%02X: %v", c.flag12, err)
		}
		if rom.TVSystem != c.want {
			t.Errorf("flag12=$%02X → TVSystem=%s; want %s",
				c.flag12, rom.TVSystem, c.want)
		}
	}
}

// CHR-RAM size from flag11 low nibble.
func TestNES2_CHRRAMSize(t *testing.T) {
	data := buildNES2(1, 0, 0x00, 0x00, 0x00, 0x00, 0x07, 0x00) // v=7 → 8 KiB
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if rom.CHRRAMSize != 8*1024 {
		t.Errorf("CHRRAMSize = %d; want 8192", rom.CHRRAMSize)
	}
}

// iNES 1.0 file (flag7 NES2 ID bits not set) still parses cleanly
// with NES2=false + zero extension fields. Catches the regression
// where NES 2.0 detection might overshoot.
func TestNES2_LegacyHeaderStaysV1(t *testing.T) {
	data := buildiNES(1, 1, 0x00, 0x00, false)
	rom, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	if rom.NES2 {
		t.Errorf("legacy header detected as NES 2.0")
	}
	if rom.SubMapper != 0 {
		t.Errorf("legacy SubMapper = %d; want 0", rom.SubMapper)
	}
	if rom.TVSystem != TVNTSC {
		t.Errorf("legacy TVSystem = %s; want NTSC default", rom.TVSystem)
	}
}
