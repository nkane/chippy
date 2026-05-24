//go:build nessy

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/nes/apu"
	"github.com/nkane/chippy/internal/nes/cart"
	"github.com/nkane/chippy/internal/nes/joypad"
	"github.com/nkane/chippy/internal/nes/ppu"
)

// stateMagic + stateVersion guard the save-state format. Bump the
// version on any breaking change to the composite struct or any
// nested *State; a forward-version save fails fast at load time.
const (
	stateMagic   = "NESSAVE\x00"
	stateVersion = 1
)

// nesSaveState is the top-level disk format. Composed of each
// subsystem's FullState plus a version envelope. Gob-encoded inside
// a gzip wrapper to keep the framebuffer-heavy PPU state on disk
// small (NES framebuffers compress 10×+).
type nesSaveState struct {
	Magic   string
	Version int
	ROMHash string // SHA-256 of the ROM bytes; tag for "wrong save for this ROM"
	CPU     cpu.FullState
	RAM     []byte
	PPU     ppu.FullState
	APU     apu.FullState
	Joypad  joypad.FullState
	Cart    cart.CartState
}

// captureNESState builds an in-memory nesSaveState from the live
// bus. The caller must hold cpuMu so all subsystems are read at a
// consistent instruction boundary.
func captureNESState(bus *nesBus, romHash string) (nesSaveState, error) {
	cs, err := cart.SaveCart(bus.cart)
	if err != nil {
		return nesSaveState{}, fmt.Errorf("cart: %w", err)
	}
	return nesSaveState{
		Magic:   stateMagic,
		Version: stateVersion,
		ROMHash: romHash,
		CPU:     bus.cpu.SaveFullState(),
		RAM:     bus.ram.SaveFullState(),
		PPU:     bus.ppu.SaveFullState(),
		APU:     bus.apu.SaveFullState(),
		Joypad:  bus.joy.SaveFullState(),
		Cart:    cs,
	}, nil
}

// applyNESState restores each subsystem from s into bus. cpuMu must
// be held. Wiring (interrupt sinks, DMC bus, frame-counter sink)
// stays connected — we only swap state, not connectivity.
func applyNESState(bus *nesBus, s nesSaveState) error {
	if s.Magic != stateMagic {
		return fmt.Errorf("save-state: bad magic %q", s.Magic)
	}
	if s.Version != stateVersion {
		return fmt.Errorf("save-state: version %d unsupported (this build expects %d)", s.Version, stateVersion)
	}
	if err := bus.ram.LoadFullState(s.RAM); err != nil {
		return fmt.Errorf("ram: %w", err)
	}
	if err := bus.ppu.LoadFullState(s.PPU); err != nil {
		return fmt.Errorf("ppu: %w", err)
	}
	if err := bus.apu.LoadFullState(s.APU); err != nil {
		return fmt.Errorf("apu: %w", err)
	}
	bus.joy.LoadFullState(s.Joypad)
	if err := cart.LoadCart(bus.cart, s.Cart); err != nil {
		return fmt.Errorf("cart: %w", err)
	}
	// CPU restore last so any side effects from the others (e.g.
	// IRQ-sink re-assertions on cart restore) don't clobber it.
	bus.cpu.LoadFullState(s.CPU)
	return nil
}

// encodeNESState gzip(gob)-encodes a save-state to disk-ready bytes.
func encodeNESState(s nesSaveState) ([]byte, error) {
	var raw bytes.Buffer
	if err := gob.NewEncoder(&raw).Encode(s); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	var out bytes.Buffer
	w := gzip.NewWriter(&out)
	if _, err := w.Write(raw.Bytes()); err != nil {
		return nil, fmt.Errorf("gzip write: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("gzip close: %w", err)
	}
	return out.Bytes(), nil
}

// decodeNESState reverses encodeNESState. Validates the magic +
// version after gob-decoding.
func decodeNESState(data []byte) (nesSaveState, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nesSaveState{}, fmt.Errorf("gzip open: %w", err)
	}
	defer func() { _ = gz.Close() }()
	var s nesSaveState
	if err := gob.NewDecoder(gz).Decode(&s); err != nil {
		return nesSaveState{}, fmt.Errorf("decode: %w", err)
	}
	return s, nil
}

// stateSlotPath returns the disk path for a given save slot keyed
// by the ROM hash. Slots 1..4 map to the F1..F4 hotkeys (load is
// Shift+F1..F4).
func stateSlotPath(romHash string, slot int) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".nessy", "states")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, fmt.Sprintf("%s-slot%d.state", romHash[:16], slot)), nil
}

// saveStateMgr coordinates async save / load against the cpuMu-
// protected bus. Hotkey handlers push a request, the game-loop
// drains it under cpuMu so we don't tear state mid-instruction.
type saveStateMgr struct {
	mu       sync.Mutex
	bus      *nesBus
	cpuMu    *sync.Mutex
	romHash  string
	pendingS int // slot to save; 0 = none
	pendingL int // slot to load; 0 = none
}

func newSaveStateMgr(bus *nesBus, cpuMu *sync.Mutex, romHash string) *saveStateMgr {
	return &saveStateMgr{bus: bus, cpuMu: cpuMu, romHash: romHash}
}

// requestSave queues a save to slot N (1-4). Picked up by serviceRequests
// on the next game-loop tick.
func (m *saveStateMgr) requestSave(slot int) {
	m.mu.Lock()
	m.pendingS = slot
	m.mu.Unlock()
}

// requestLoad queues a load from slot N (1-4).
func (m *saveStateMgr) requestLoad(slot int) {
	m.mu.Lock()
	m.pendingL = slot
	m.mu.Unlock()
}

// serviceRequests handles any queued save / load. Called from the
// game loop's Update under cpuMu. Errors land on stderr (best-effort
// — a busted save shouldn't crash the running game).
func (m *saveStateMgr) serviceRequests() {
	m.mu.Lock()
	saveSlot, loadSlot := m.pendingS, m.pendingL
	m.pendingS, m.pendingL = 0, 0
	m.mu.Unlock()

	if saveSlot != 0 {
		path, err := stateSlotPath(m.romHash, saveSlot)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nessy: save-state path:", err)
		} else {
			s, err := captureNESState(m.bus, m.romHash)
			if err != nil {
				fmt.Fprintln(os.Stderr, "nessy: capture:", err)
			} else if data, err := encodeNESState(s); err != nil {
				fmt.Fprintln(os.Stderr, "nessy: encode:", err)
			} else if err := os.WriteFile(path, data, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, "nessy: save-state write:", err)
			} else {
				fmt.Fprintf(os.Stderr, "nessy: saved slot %d -> %s\n", saveSlot, path)
			}
		}
	}

	if loadSlot != 0 {
		path, err := stateSlotPath(m.romHash, loadSlot)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nessy: load-state path:", err)
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nessy: load-state read:", err)
			return
		}
		s, err := decodeNESState(data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "nessy: decode:", err)
			return
		}
		if s.ROMHash != m.romHash {
			fmt.Fprintf(os.Stderr, "nessy: load-state slot %d: wrong ROM (saved for %s; this is %s)\n", loadSlot, s.ROMHash[:8], m.romHash[:8])
			return
		}
		if err := applyNESState(m.bus, s); err != nil {
			fmt.Fprintln(os.Stderr, "nessy: apply:", err)
			return
		}
		fmt.Fprintf(os.Stderr, "nessy: loaded slot %d <- %s\n", loadSlot, path)
	}
}
