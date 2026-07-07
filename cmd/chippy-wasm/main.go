//go:build js && wasm

// chippy-wasm is the browser entry point for the chippy 6502 emulator.
// Build with:
//
//	GOOS=js GOARCH=wasm go build -o web/chippy.wasm ./cmd/chippy-wasm
//
// The page's JS shell loads chippy.wasm + Go's wasm_exec.js, then
// drives the emulator through the `chippy` global this file installs.
// See web/index.html for the host page.
package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/peripheral"
)

var (
	ram     *cpu.RAM
	mmio    *cpu.MMIO
	c       *cpu.CPU
	banked  *cpu.Banked24 // 65816 bank-aware bus; nil for 8/16-bit variants
	textOut *peripheral.TextOutput
	keyIn   *peripheral.KeyboardInput
	variant = cpu.VariantNMOS
)

func main() {
	rebuild()
	js.Global().Set("chippy", makeAPI())
	// Keep the WASM module alive so the JS side can keep calling in.
	select {}
}

// rebuild constructs a fresh CPU + RAM + MMIO + peripherals. Called at
// boot and on every reset / variant change so peripherals re-attach
// cleanly to the new RAM.
func rebuild() {
	ram = cpu.NewRAM()
	mmio = cpu.NewMMIO(ram)
	textOut = peripheral.NewTextOutput(0xF001)
	keyIn = peripheral.NewKeyboardInput(0xF004, 0xF005)
	_ = mmio.Register(textOut)
	_ = mmio.Register(keyIn)
	c = cpu.NewVariant(mmio, variant)
	// The 65816 reads/writes through a 24-bit bus. Banked24 routes bank 0
	// through MMIO (so the playground's readMem/disasm panes stay accurate) and
	// backs banks 1-255 with real storage (#505).
	banked = nil
	if variant == cpu.VariantW65816 {
		banked = cpu.NewBanked24(mmio)
		c.SetBus24(banked)
	}
}

func makeAPI() js.Value {
	api := js.Global().Get("Object").New()
	api.Set("load", js.FuncOf(jsLoad))
	api.Set("reset", js.FuncOf(jsReset))
	api.Set("step", js.FuncOf(jsStep))
	api.Set("run", js.FuncOf(jsRun))
	api.Set("state", js.FuncOf(jsState))
	api.Set("readMem", js.FuncOf(jsReadMem))
	api.Set("writeMem", js.FuncOf(jsWriteMem))
	api.Set("disasm", js.FuncOf(jsDisasm))
	api.Set("textOutput", js.FuncOf(jsTextOutput))
	api.Set("clearTextOutput", js.FuncOf(jsClearTextOutput))
	api.Set("pushKey", js.FuncOf(jsPushKey))
	api.Set("setVariant", js.FuncOf(jsSetVariant))
	return api
}

// jsLoad: (bytesUint8Array, opts) -> {ok, loadAddr, size, format, error?}
// opts.format: "bin" | "prg" | "hex" — bin defaults loadAddr to opts.addr or $8000.
func jsLoad(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return errObj("load expects (bytes, opts?)")
	}
	raw := jsBytes(args[0])
	opts := js.Value{}
	if len(args) >= 2 {
		opts = args[1]
	}

	format := "bin"
	if !opts.IsUndefined() && !opts.IsNull() {
		if f := opts.Get("format"); !f.IsUndefined() && !f.IsNull() {
			format = strings.ToLower(f.String())
		}
	}
	if format == "" {
		format = "bin"
	}

	var loadAddr uint16
	var size int
	var err error
	switch format {
	case "bin":
		addr := uint16(0x8000)
		if !opts.IsUndefined() && !opts.IsNull() {
			if a := opts.Get("addr"); a.Type() == js.TypeNumber {
				addr = uint16(a.Int())
			}
		}
		loadAddr, size, err = loadBin(raw, addr)
	case "prg":
		loadAddr, size, err = loadPRG(raw)
	case "hex":
		loadAddr, size, err = loadHEX(raw)
	default:
		err = fmt.Errorf("unknown format %q", format)
	}
	if err != nil {
		return errObj(err.Error())
	}

	// Seed the reset vector to the load address if it's still zeroed
	// AND the caller didn't override it. Matches the CLI's default
	// behavior so canned demos boot without extra JS plumbing.
	if !opts.IsUndefined() && !opts.IsNull() {
		if rv := opts.Get("resetVec"); rv.Type() == js.TypeNumber && rv.Int() != 0 {
			v := uint16(rv.Int())
			ram.Write(cpu.VecReset, byte(v))
			ram.Write(cpu.VecReset+1, byte(v>>8))
		} else if ram.Read(cpu.VecReset) == 0 && ram.Read(cpu.VecReset+1) == 0 {
			ram.Write(cpu.VecReset, byte(loadAddr))
			ram.Write(cpu.VecReset+1, byte(loadAddr>>8))
		}
	} else if ram.Read(cpu.VecReset) == 0 && ram.Read(cpu.VecReset+1) == 0 {
		ram.Write(cpu.VecReset, byte(loadAddr))
		ram.Write(cpu.VecReset+1, byte(loadAddr>>8))
	}
	c.Reset()

	out := js.Global().Get("Object").New()
	out.Set("ok", true)
	out.Set("loadAddr", int(loadAddr))
	out.Set("size", size)
	out.Set("format", format)
	return out
}

func jsReset(this js.Value, args []js.Value) interface{} {
	c.Reset()
	return nil
}

func jsStep(this js.Value, args []js.Value) interface{} {
	cycles := c.Step()
	out := js.Global().Get("Object").New()
	out.Set("cycles", cycles)
	out.Set("pc", int(c.PC))
	out.Set("halted", c.Halted)
	return out
}

// jsRun: (maxSteps) — runs up to maxSteps instructions or until the CPU
// halts. Returns the actual step count and halt flag. Step budget keeps
// the browser tab from freezing under tight loops; the caller is
// expected to schedule the next batch via requestAnimationFrame.
func jsRun(this js.Value, args []js.Value) interface{} {
	max := 100_000
	if len(args) >= 1 && args[0].Type() == js.TypeNumber {
		max = args[0].Int()
		if max <= 0 {
			max = 1
		}
	}
	steps := 0
	for steps < max && !c.Halted {
		c.Step()
		steps++
	}
	out := js.Global().Get("Object").New()
	out.Set("steps", steps)
	out.Set("halted", c.Halted)
	out.Set("pc", int(c.PC))
	return out
}

func jsState(this js.Value, args []js.Value) interface{} {
	out := js.Global().Get("Object").New()
	out.Set("a", int(c.A))
	out.Set("x", int(c.X))
	out.Set("y", int(c.Y))
	out.Set("sp", int(c.SP))
	out.Set("p", int(c.P))
	out.Set("pc", int(c.PC))
	out.Set("cycles", float64(c.Cycles))
	out.Set("halted", c.Halted)
	flags := js.Global().Get("Object").New()
	flags.Set("n", c.P&cpu.FlagN != 0)
	flags.Set("v", c.P&cpu.FlagV != 0)
	flags.Set("b", c.P&cpu.FlagB != 0)
	flags.Set("d", c.P&cpu.FlagD != 0)
	flags.Set("i", c.P&cpu.FlagI != 0)
	flags.Set("z", c.P&cpu.FlagZ != 0)
	flags.Set("c", c.P&cpu.FlagC != 0)
	out.Set("flags", flags)
	return out
}

// jsReadMem: (addr, len) -> Uint8Array
func jsReadMem(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return js.Null()
	}
	addr := args[0].Int() & 0xFFFF
	n := args[1].Int()
	if n < 0 {
		n = 0
	}
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		buf[i] = ram.Read(uint16((addr + i) & 0xFFFF))
	}
	out := js.Global().Get("Uint8Array").New(n)
	js.CopyBytesToJS(out, buf)
	return out
}

// jsWriteMem: (addr, byteOrUint8Array)
func jsWriteMem(this js.Value, args []js.Value) interface{} {
	if len(args) < 2 {
		return nil
	}
	addr := args[0].Int() & 0xFFFF
	v := args[1]
	if v.Type() == js.TypeNumber {
		ram.Write(uint16(addr), byte(v.Int()))
		return nil
	}
	// Assume Uint8Array.
	n := v.Get("length").Int()
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, v)
	for i, b := range buf {
		ram.Write(uint16((addr+i)&0xFFFF), b)
	}
	return nil
}

// jsDisasm: (addr, count) -> [{addr, bytes, text, size}]
func jsDisasm(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return js.Global().Get("Array").New()
	}
	addr := uint16(args[0].Int() & 0xFFFF)
	count := 16
	if len(args) >= 2 && args[1].Type() == js.TypeNumber {
		count = args[1].Int()
	}
	out := js.Global().Get("Array").New()
	cur := addr
	for i := 0; i < count; i++ {
		text, size := cpu.DisasmCPU(c, cur)
		row := js.Global().Get("Object").New()
		row.Set("addr", int(cur))
		bytesArr := js.Global().Get("Uint8Array").New(size)
		buf := make([]byte, size)
		for j := 0; j < size; j++ {
			buf[j] = ram.Read(uint16(int(cur)+j) & 0xFFFF)
		}
		js.CopyBytesToJS(bytesArr, buf)
		row.Set("bytes", bytesArr)
		row.Set("text", text)
		row.Set("size", size)
		out.Call("push", row)
		cur += uint16(size)
	}
	return out
}

func jsTextOutput(this js.Value, args []js.Value) interface{} {
	return textOut.String()
}

func jsClearTextOutput(this js.Value, args []js.Value) interface{} {
	textOut.Reset()
	return nil
}

func jsPushKey(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 || args[0].Type() != js.TypeNumber {
		return nil
	}
	keyIn.Push(byte(args[0].Int()))
	return nil
}

// jsSetVariant: (string "nmos"|"65c02"|"nes"|"65816") — rebuilds the world so
// the new opcode table (and, for the 65816, the bank-aware 24-bit bus) takes
// effect. The caller is expected to re-load the ROM.
func jsSetVariant(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return errObj("setVariant expects a name")
	}
	switch strings.ToLower(args[0].String()) {
	case "nmos", "6502", "":
		variant = cpu.VariantNMOS
	case "65c02", "cmos", "cmos65c02":
		variant = cpu.VariantCMOS65C02
	case "nes", "2a03", "ricoh":
		variant = cpu.VariantNES
	case "65816", "65c816", "w65816":
		variant = cpu.VariantW65816
	default:
		return errObj("unknown variant " + args[0].String())
	}
	rebuild()
	return nil
}

// ---------- helpers ----------

func errObj(msg string) js.Value {
	out := js.Global().Get("Object").New()
	out.Set("ok", false)
	out.Set("error", msg)
	return out
}

func jsBytes(v js.Value) []byte {
	n := v.Get("length").Int()
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, v)
	return buf
}

func loadBin(data []byte, addr uint16) (uint16, int, error) {
	if int(addr)+len(data) > 0x10000 {
		return 0, 0, fmt.Errorf("bin: load %d bytes at $%04X exceeds 64KB", len(data), addr)
	}
	ram.Load(addr, data)
	return addr, len(data), nil
}

func loadPRG(data []byte) (uint16, int, error) {
	if len(data) < 2 {
		return 0, 0, fmt.Errorf("prg: file too short (%d bytes)", len(data))
	}
	addr := uint16(data[0]) | uint16(data[1])<<8
	body := data[2:]
	if int(addr)+len(body) > 0x10000 {
		return 0, 0, fmt.Errorf("prg: load %d bytes at $%04X exceeds 64KB", len(body), addr)
	}
	ram.Load(addr, body)
	return addr, len(body), nil
}

// loadHEX is a stripped-down Intel-HEX reader: record types 00 (data)
// and 01 (EOF) only. Mirrors internal/loader's behavior but reads from
// a byte slice instead of a file handle.
func loadHEX(data []byte) (uint16, int, error) {
	var minAddr uint32 = 0xFFFFFFFF
	var maxAddr uint32
	total := 0
	scan := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for scan.Scan() {
		lineNo++
		line := strings.TrimSpace(scan.Text())
		if line == "" {
			continue
		}
		if line[0] != ':' {
			return 0, 0, fmt.Errorf("hex: line %d missing ':'", lineNo)
		}
		raw, err := hex.DecodeString(line[1:])
		if err != nil || len(raw) < 5 {
			return 0, 0, fmt.Errorf("hex: line %d malformed", lineNo)
		}
		count := int(raw[0])
		addr := uint16(raw[1])<<8 | uint16(raw[2])
		typ := raw[3]
		if len(raw) != 5+count {
			return 0, 0, fmt.Errorf("hex: line %d byte count mismatch", lineNo)
		}
		// Skip checksum verify — corrupt hex files in the browser are
		// already a user-visible failure mode; one less per-line cost.
		switch typ {
		case 0x00:
			payload := raw[4 : 4+count]
			if int(addr)+len(payload) > 0x10000 {
				return 0, 0, fmt.Errorf("hex: line %d would overflow 64KB", lineNo)
			}
			ram.Load(addr, payload)
			a := uint32(addr)
			if a < minAddr {
				minAddr = a
			}
			if a+uint32(len(payload)) > maxAddr {
				maxAddr = a + uint32(len(payload))
			}
			total += len(payload)
		case 0x01:
			if minAddr == 0xFFFFFFFF {
				minAddr = 0
			}
			return uint16(minAddr), total, nil
		}
	}
	if err := scan.Err(); err != nil {
		return 0, 0, fmt.Errorf("hex: read: %w", err)
	}
	if minAddr == 0xFFFFFFFF {
		minAddr = 0
	}
	return uint16(minAddr), total, nil
}
