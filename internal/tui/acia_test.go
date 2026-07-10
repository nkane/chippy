package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/peripheral"
)

func keyMsg(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// WithACIA wires the peripheral and, with no TextOutput present, the Output
// pane sources its content from the ACIA's TX sink.
func TestACIA_WiringAndOutputSource(t *testing.T) {
	m, _ := stateTestModel(t)
	if m.hasOutput() {
		t.Fatal("no output devices wired yet")
	}

	acia := peripheral.NewACIA(0x5000)
	m2 := m.WithACIA(acia)
	if m2.ACIA != acia {
		t.Fatal("WithACIA did not attach the peripheral")
	}
	if !m2.hasOutput() {
		t.Fatal("ACIA should light up the Output pane")
	}
	acia.Write(0x5000, 'h')
	acia.Write(0x5000, 'i')
	if got := m2.outputText(); got != "hi" {
		t.Errorf("outputText from ACIA = %q; want %q", got, "hi")
	}

	// TextOutput wins as the source when both are present.
	m3 := m2.WithTextOutput(peripheral.NewTextOutput(0xF001))
	m3.TextOut.Write(0xF001, 'X')
	if got := m3.outputText(); got != "X" {
		t.Errorf("outputText should prefer TextOutput; got %q", got)
	}
}

// InputMode routes keystrokes to the ACIA's RX queue when no keyboard is
// wired, so a serial ROM sees typed input.
func TestACIA_InputModeRoutesToRx(t *testing.T) {
	m, _ := stateTestModel(t)
	acia := peripheral.NewACIA(0x5000)
	mm := m.WithACIA(acia)

	updated, _ := mm.updateInputMode(keyMsg('A'))
	next := updated.(Model)
	if next.ACIA.RxPending() != 1 {
		t.Fatalf("keystroke not queued to ACIA; RxPending=%d", next.ACIA.RxPending())
	}
	if got := acia.Read(0x5000); got != 'A' {
		t.Errorf("ACIA RX byte = %q; want 'A'", got)
	}
}

// The ACIA participates in the reverse-step peripheral snapshot map, keyed by
// its base address, so rewind restores serial state.
func TestACIA_SnapshotRoundTrip(t *testing.T) {
	m, _ := stateTestModel(t)
	acia := peripheral.NewACIA(0x5000)
	mm := m.WithACIA(acia)

	acia.Receive('Z')
	acia.Write(0x5000, 'q')

	var snap cpu.Snapshot
	mm.captureperipherals(&snap)
	if _, ok := snap.Peripherals["$5000"]; !ok {
		t.Fatal("ACIA state not captured under its base key")
	}

	// Mutate, then restore the snapshot — state must revert.
	acia.Read(0x5000) // drain the RX byte
	acia.Write(0x5000, 'x')
	mm.restoreperipherals(snap)
	if acia.RxPending() != 1 {
		t.Errorf("RX not restored; RxPending=%d want 1", acia.RxPending())
	}
	if got := acia.TxString(); got != "q" {
		t.Errorf("TX not restored; got %q want %q", got, "q")
	}
}
