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

	"github.com/nkane/chippy/cpu"
)

// Result describes what was loaded and where.
type Result struct {
	LoadAddr uint16 // 16-bit offset (within Bank) bytes were placed at
	Bank     byte   // 65816 bank of the first byte (0 for 16-bit formats)
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
	// Bus24, when set, is the 65816's bank-aware bus. Intel HEX records beyond
	// bank 0 (via type-04 Extended Linear Address) load through it; bank 0 still
	// loads into ram directly (bypassing MMIO, per the loader invariant). nil
	// for non-65816 runs — a beyond-bank-0 record then errors.
	Bus24 cpu.Bus24
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
		return loadHEX(ram, path, opt)
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

// loadHEX parses Intel HEX. Supported record types: 00 (data), 01 (EOF),
// 04 (Extended Linear Address — the upper 16 bits of a 24-bit address). A
// type-04 base lifts subsequent data records into banks 1-255: bank 0 loads
// into ram directly (bypassing MMIO, per the loader invariant); beyond bank 0
// loads through opt.Bus24 (the 65816 bank-aware bus). Without Bus24 a
// beyond-bank-0 record is an error.
func loadHEX(ram *cpu.RAM, path string, opt Options) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var base uint32 // upper bits from the most recent type-04 record
	var minAddr uint32 = 0xFFFFFFFF
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
			eff := base + uint32(addr)
			if eff>>24 != 0 || (eff&0xFFFF)+uint32(len(data)) > 0x10000 {
				return nil, fmt.Errorf("hex: line %d data overflows its bank at $%06X", lineNo, eff)
			}
			if eff < 0x10000 {
				ram.Load(uint16(eff), data)
			} else {
				if opt.Bus24 == nil {
					return nil, fmt.Errorf("hex: line %d targets bank $%02X but no 65816 bus is wired (use -cpu 65816)", lineNo, eff>>16)
				}
				for i, b := range data {
					opt.Bus24.Write24(eff+uint32(i), b)
				}
			}
			total += len(data)
			if eff < minAddr {
				minAddr = eff
			}
		case 0x04: // extended linear address: upper 16 bits of a 32-bit base
			if count != 2 {
				return nil, fmt.Errorf("hex: line %d type-04 must carry 2 bytes", lineNo)
			}
			base = uint32(raw[4])<<24 | uint32(raw[5])<<16
		case 0x01: // EOF
			return hexResult(minAddr, total), nil
		default:
			// ignore record types we don't model (e.g. 02/03/05)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return hexResult(minAddr, total), nil
}

func hexResult(minAddr uint32, total int) *Result {
	if minAddr == 0xFFFFFFFF {
		minAddr = 0
	}
	return &Result{LoadAddr: uint16(minAddr), Bank: byte(minAddr >> 16), Size: total, Format: "hex"}
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
