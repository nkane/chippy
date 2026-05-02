// Package loader handles input file detection and loading into the CPU bus.
//
// Supported formats (auto-detected by extension):
//
//	.bin  raw binary, placed at -addr (default $8000)
//	.prg  Commodore-style: first 2 bytes = little-endian load address
//	.hex  Intel HEX (records of type 00 data, 01 EOF)
//	.o    cc65/ca65 object file. Requires `ld65` on PATH and -cfg <linker.cfg>.
//	      The .o is linked in a temp dir and the resulting .bin is loaded.
package loader

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nkane/chippy/internal/cpu"
)

// Result describes what was loaded and where.
type Result struct {
	LoadAddr uint16 // address bytes were placed at
	Size     int    // number of bytes loaded
	Format   string // detected format, for status display
	// LinkedBin is set when a .o was linked; points to the produced .bin file
	// so a sibling .dbg can be located.
	LinkedBin string
}

// Options controls loader behavior.
type Options struct {
	// Addr is the load address used for raw .bin files (ignored by .prg/.hex/.o).
	Addr uint16
	// LinkerCfg is the path to a ld65 .cfg file. Required for .o input.
	LinkerCfg string
}

// Load reads `path`, detects format, and writes bytes into `ram`.
func Load(ram *cpu.RAM, path string, opt Options) (*Result, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".bin", "":
		return loadBin(ram, path, opt.Addr)
	case ".prg":
		return loadPRG(ram, path)
	case ".hex":
		return loadHEX(ram, path)
	case ".o":
		return loadObject(ram, path, opt)
	default:
		// Unknown extension — try raw bin at opt.Addr.
		return loadBin(ram, path, opt.Addr)
	}
}

func loadBin(ram *cpu.RAM, path string, addr uint16) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if int(addr)+len(data) > 0x10000 {
		return nil, fmt.Errorf("bin: load %d bytes at $%04X exceeds 64KB", len(data), addr)
	}
	ram.Load(addr, data)
	return &Result{LoadAddr: addr, Size: len(data), Format: "bin"}, nil
}

func loadPRG(ram *cpu.RAM, path string) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 2 {
		return nil, fmt.Errorf("prg: file too short (%d bytes)", len(data))
	}
	addr := uint16(data[0]) | uint16(data[1])<<8
	body := data[2:]
	if int(addr)+len(body) > 0x10000 {
		return nil, fmt.Errorf("prg: load %d bytes at $%04X exceeds 64KB", len(body), addr)
	}
	ram.Load(addr, body)
	return &Result{LoadAddr: addr, Size: len(body), Format: "prg"}, nil
}

// loadHEX parses Intel HEX. Supported record types: 00 (data), 01 (EOF).
// Other types (extended segment/linear address) are ignored — 6502 is 16-bit.
func loadHEX(ram *cpu.RAM, path string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var minAddr uint32 = 0xFFFFFFFF
	var maxAddr uint32
	total := 0
	scan := bufio.NewScanner(f)
	lineNo := 0
	for scan.Scan() {
		lineNo++
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		if line[0] != ':' {
			return nil, fmt.Errorf("hex: line %d missing ':'", lineNo)
		}
		raw, err := hex.DecodeString(line[1:])
		if err != nil {
			return nil, fmt.Errorf("hex: line %d: %w", lineNo, err)
		}
		if len(raw) < 5 {
			return nil, fmt.Errorf("hex: line %d too short", lineNo)
		}
		count := raw[0]
		if int(count)+5 != len(raw) {
			return nil, fmt.Errorf("hex: line %d length mismatch", lineNo)
		}
		// checksum: two's complement of sum of all bytes except checksum
		var sum byte
		for _, b := range raw[:len(raw)-1] {
			sum += b
		}
		if byte(-int8(sum)) != raw[len(raw)-1] {
			return nil, fmt.Errorf("hex: line %d bad checksum", lineNo)
		}
		addr := uint16(raw[1])<<8 | uint16(raw[2])
		rec := raw[3]
		switch rec {
		case 0x00: // data
			data := raw[4 : 4+count]
			if int(addr)+len(data) > 0x10000 {
				return nil, fmt.Errorf("hex: line %d data overflows 64KB", lineNo)
			}
			ram.Load(addr, data)
			total += len(data)
			a := uint32(addr)
			if a < minAddr {
				minAddr = a
			}
			if a+uint32(len(data)) > maxAddr {
				maxAddr = a + uint32(len(data))
			}
		case 0x01: // EOF
			if minAddr == 0xFFFFFFFF {
				minAddr = 0
			}
			return &Result{LoadAddr: uint16(minAddr), Size: total, Format: "hex"}, nil
		default:
			// ignore record types we don't care about for 6502
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if minAddr == 0xFFFFFFFF {
		minAddr = 0
	}
	return &Result{LoadAddr: uint16(minAddr), Size: total, Format: "hex"}, nil
}

// loadObject links a ca65/cc65 .o using ld65 and loads the resulting binary.
// Requires opt.LinkerCfg to be set and `ld65` to be on PATH.
func loadObject(ram *cpu.RAM, path string, opt Options) (*Result, error) {
	if opt.LinkerCfg == "" {
		return nil, fmt.Errorf("o: ca65 object files must be linked. Pass -cfg <linker.cfg>, or run ld65 yourself and load the .bin")
	}
	if _, err := exec.LookPath("ld65"); err != nil {
		return nil, fmt.Errorf("o: ld65 not found on PATH (install cc65)")
	}

	tmp, err := os.MkdirTemp("", "chippy-link-*")
	if err != nil {
		return nil, err
	}
	binOut := filepath.Join(tmp, "out.bin")
	dbgOut := filepath.Join(tmp, "out.dbg")

	cmd := exec.Command("ld65",
		"-C", opt.LinkerCfg,
		"-o", binOut,
		"--dbgfile", dbgOut,
		path,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("ld65 failed: %v\n%s", err, out)
	}

	res, err := loadBin(ram, binOut, opt.Addr)
	if err != nil {
		return nil, err
	}
	res.Format = "o (ld65-linked)"
	res.LinkedBin = binOut
	return res, nil
}
