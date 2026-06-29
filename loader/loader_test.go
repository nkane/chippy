package loader

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nkane/chippy/cpu"
)

// hexLine builds one Intel HEX record (with a correct checksum) for tests.
func hexLine(rec byte, addr uint16, data []byte) string {
	raw := []byte{byte(len(data)), byte(addr >> 8), byte(addr), rec}
	raw = append(raw, data...)
	var sum byte
	for _, b := range raw {
		sum += b
	}
	raw = append(raw, byte(-int8(sum)))
	return ":" + strings.ToUpper(hex.EncodeToString(raw)) + "\n"
}

func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadBin(t *testing.T) {
	ram := cpu.NewRAM()
	p := writeTemp(t, "x.bin", []byte{0xAA, 0xBB, 0xCC})
	r, err := Load(ram, p, Options{Addr: 0x8000})
	if err != nil {
		t.Fatal(err)
	}
	if r.LoadAddr != 0x8000 || r.Size != 3 {
		t.Fatalf("got %+v", r)
	}
	if ram.Read(0x8000) != 0xAA || ram.Read(0x8002) != 0xCC {
		t.Fatal("bytes not placed")
	}
}

func TestLoadPRG(t *testing.T) {
	ram := cpu.NewRAM()
	// load addr $C000, then payload
	data := []byte{0x00, 0xC0, 0x12, 0x34, 0x56}
	p := writeTemp(t, "x.prg", data)
	r, err := Load(ram, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.LoadAddr != 0xC000 || r.Size != 3 {
		t.Fatalf("got %+v", r)
	}
	if ram.Read(0xC000) != 0x12 || ram.Read(0xC002) != 0x56 {
		t.Fatal("payload wrong")
	}
}

func TestLoadHEX(t *testing.T) {
	// :03 8000 00 A9 42 00 sum
	// bytes summed: 03+80+00+00+A9+42+00 = 0x16E -> low = 0x6E -> two's comp = 0x92
	// Plus EOF :00000001FF
	hex := ":038000 00 A94200 92\n:00000001 FF\n"
	// remove spaces (we used spaces for clarity)
	clean := ""
	for _, ch := range hex {
		if ch != ' ' {
			clean += string(ch)
		}
	}
	ram := cpu.NewRAM()
	p := writeTemp(t, "x.hex", []byte(clean))
	r, err := Load(ram, p, Options{})
	if err != nil {
		t.Fatalf("hex parse: %v", err)
	}
	if r.LoadAddr != 0x8000 || r.Size != 3 {
		t.Fatalf("got %+v", r)
	}
	if ram.Read(0x8000) != 0xA9 || ram.Read(0x8001) != 0x42 || ram.Read(0x8002) != 0x00 {
		t.Fatalf("hex bytes wrong: %02X %02X %02X",
			ram.Read(0x8000), ram.Read(0x8001), ram.Read(0x8002))
	}
}

func TestLoadHEX_ExtendedLinearAddress(t *testing.T) {
	// type-04 ELA = $0002 lifts the base to bank 2; the data record at $9000
	// then lands at $029000 through the bank-aware bus, not bank 0.
	doc := hexLine(0x04, 0x0000, []byte{0x00, 0x02}) +
		hexLine(0x00, 0x9000, []byte{0xDE, 0xAD}) +
		hexLine(0x01, 0x0000, nil)

	ram := cpu.NewRAM()
	banked := cpu.NewBanked24(ram)
	p := writeTemp(t, "banked.hex", []byte(doc))
	r, err := Load(ram, p, Options{Bus24: banked})
	if err != nil {
		t.Fatalf("hex parse: %v", err)
	}
	if r.Bank != 0x02 || r.LoadAddr != 0x9000 || r.Size != 2 {
		t.Fatalf("got %+v", r)
	}
	if got := banked.Read24(0x029000); got != 0xDE {
		t.Fatalf("bank 2 $9000 = $%02X want $DE", got)
	}
	if got := banked.Read24(0x029001); got != 0xAD {
		t.Fatalf("bank 2 $9001 = $%02X want $AD", got)
	}
	// The bytes must not have aliased into bank 0.
	if ram.Read(0x9000) != 0x00 {
		t.Fatalf("banked load leaked into bank 0: ram[$9000]=$%02X", ram.Read(0x9000))
	}
}

func TestLoadHEX_BankedWithoutBus(t *testing.T) {
	// A beyond-bank-0 record without a 65816 bus wired is an error, not a
	// silent bank-0 alias.
	doc := hexLine(0x04, 0x0000, []byte{0x00, 0x01}) +
		hexLine(0x00, 0x8000, []byte{0x42}) +
		hexLine(0x01, 0x0000, nil)
	ram := cpu.NewRAM()
	p := writeTemp(t, "nobus.hex", []byte(doc))
	if _, err := Load(ram, p, Options{}); err == nil {
		t.Fatal("expected error loading bank 1 with no Bus24")
	}
}

func TestLoadObject_NoConfig(t *testing.T) {
	ram := cpu.NewRAM()
	p := writeTemp(t, "x.o", []byte{0})
	_, err := Load(ram, p, Options{})
	if err == nil {
		t.Fatal("expected error when -cfg missing")
	}
}
