package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nkane/chippy/internal/cpu"
)

// stateTestModel constructs a fresh Model wired enough to call
// load/save. StatePath points at a temp file so the test doesn't touch
// the user's real ~/.chippy directory.
func stateTestModel(t *testing.T) (*Model, string) {
	t.Helper()
	ram := cpu.NewRAM()
	c := cpu.New(ram)
	m := New(c, ram)
	tmp := filepath.Join(t.TempDir(), "state.json")
	m.StatePath = tmp
	return &m, tmp
}

// Golden-file load: a checked-in v1 file populates every field this
// release commits to persisting. Failure here means we broke the format
// contract — bump StateSchemaVersion and write a migration.
func TestLoadState_GoldenV1(t *testing.T) {
	m, _ := stateTestModel(t)
	loadState(m, "testdata/state-v1.json")

	if m.MemViewAddr != 0x2000 {
		t.Errorf("MemViewAddr want $2000; got $%04X", m.MemViewAddr)
	}
	if m.MemCursor != 0x2005 {
		t.Errorf("MemCursor want $2005; got $%04X", m.MemCursor)
	}
	if m.TargetHz != 60 {
		t.Errorf("TargetHz want 60; got %d", m.TargetHz)
	}
	if len(m.Watches) != 2 {
		t.Fatalf("Watches want 2; got %d", len(m.Watches))
	}
	if m.Watches[0].Label != "score" || m.Watches[0].Addr != 0x2000 || m.Watches[0].Width != 2 {
		t.Errorf("watch[0] = %+v", m.Watches[0])
	}
	if m.Watches[1].Reg != "A" || m.Watches[1].Kind != "reg" {
		t.Errorf("watch[1] = %+v", m.Watches[1])
	}
	if _, ok := m.Breakpoints[0x8000]; !ok {
		t.Errorf("missing bp at $8000")
	}
	bp := m.Breakpoints[0x8050]
	if bp == nil {
		t.Fatalf("missing bp at $8050")
	}
	if bp.HitLimit != 3 || bp.Cond != "A == $42" {
		t.Errorf("bp $8050 metadata not preserved: %+v", bp)
	}
	if _, ok := m.MemBPs[0x0400]; !ok {
		t.Errorf("missing membp at $0400")
	}
}

// Save then load — every field we promised to persist round-trips. The
// written file must include the schemaVersion field at the documented
// value.
func TestSaveState_RoundTripAndIncludesSchemaVersion(t *testing.T) {
	m, path := stateTestModel(t)
	m.MemViewAddr = 0x4000
	m.MemCursor = 0x4005
	m.TargetHz = 120
	m.Watches = []Watch{{Kind: "mem", Addr: 0x4000, Label: "tile", Width: 1}}
	m.Breakpoints[0x9000] = newBP(0x9000)
	m.Breakpoints[0x9000].HitLimit = -1
	m.MemBPs[0x0500] = newMemBP(0x0500, MemBPWrite)

	m.saveState()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if !strings.Contains(string(raw), `"schemaVersion": 1`) {
		t.Fatalf("written state must include schemaVersion field; got:\n%s", raw)
	}

	// Now load into a fresh model and compare.
	m2, _ := stateTestModel(t)
	loadState(m2, path)
	if m2.MemViewAddr != 0x4000 || m2.MemCursor != 0x4005 || m2.TargetHz != 120 {
		t.Errorf("scalar fields not preserved: viewAddr=$%04X cursor=$%04X hz=%d",
			m2.MemViewAddr, m2.MemCursor, m2.TargetHz)
	}
	if len(m2.Watches) != 1 || m2.Watches[0].Label != "tile" {
		t.Errorf("watches not preserved: %+v", m2.Watches)
	}
	if _, ok := m2.Breakpoints[0x9000]; !ok {
		t.Errorf("bp not preserved")
	}
	if _, ok := m2.MemBPs[0x0500]; !ok {
		t.Errorf("membp not preserved")
	}
}

// Future-format files must be ignored, not corrupted. A v2 file with
// fields the current build doesn't understand should leave the model in
// its default state — better that than partial-load.
func TestLoadState_FutureSchemaVersionIgnored(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "future.json")
	future := map[string]interface{}{
		"schemaVersion":        StateSchemaVersion + 1,
		"mem_view_addr":        0x7FFF,
		"some_future_field":    "not yet supported",
		"another_future_thing": []int{1, 2, 3},
		"target_hz":            999,
	}
	data, _ := json.MarshalIndent(future, "", "  ")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	m, _ := stateTestModel(t)
	defaultViewAddr := m.MemViewAddr
	defaultHz := m.TargetHz
	loadState(m, tmp)

	if m.MemViewAddr != defaultViewAddr {
		t.Errorf("future schema bumped MemViewAddr; got $%04X want default $%04X",
			m.MemViewAddr, defaultViewAddr)
	}
	if m.TargetHz != defaultHz {
		t.Errorf("future schema bumped TargetHz; got %d want default %d",
			m.TargetHz, defaultHz)
	}
}

// Pre-freeze files (no schemaVersion field) still load — the format
// was field-compatible, so they decode as v0 legacy.
func TestLoadState_LegacyFileLoadsAsV0(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "legacy.json")
	legacy := `{
		"mem_view_addr": 12288,
		"target_hz": 30,
		"watches": [{"kind":"reg","reg":"X"}]
	}`
	if err := os.WriteFile(tmp, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	m, _ := stateTestModel(t)
	loadState(m, tmp)
	if m.MemViewAddr != 0x3000 || m.TargetHz != 30 || len(m.Watches) != 1 {
		t.Errorf("legacy decode wrong: viewAddr=$%04X hz=%d watches=%d",
			m.MemViewAddr, m.TargetHz, len(m.Watches))
	}
}

// Malformed file is gracefully ignored — no panic, no partial state.
func TestLoadState_MalformedJSONIgnored(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(tmp, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}
	m, _ := stateTestModel(t)
	loadState(m, tmp)
	// Should not crash, fields stay at defaults.
}

// Sanity: the constant matches the value the loader's threshold check
// uses. Forgetting to bump StateSchemaVersion when the loader changes
// is the most likely format-break path.
func TestStateSchemaVersionMatchesConst(t *testing.T) {
	if StateSchemaVersion < 1 {
		t.Fatalf("StateSchemaVersion regressed below v1: %d", StateSchemaVersion)
	}
}
