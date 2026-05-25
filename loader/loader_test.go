package loader

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/cpu"
)

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

func TestLoadObject_NoConfig(t *testing.T) {
	ram := cpu.NewRAM()
	p := writeTemp(t, "x.o", []byte{0})
	_, err := Load(ram, p, Options{})
	if err == nil {
		t.Fatal("expected error when -cfg missing")
	}
}
