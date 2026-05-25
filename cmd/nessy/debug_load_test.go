package main

import (
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/symbols"
)

// hello-bg ships a built `.dbg` next to its `.nes` (issue #211). nessy
// auto-detects it via `symbols.SiblingDbg`; that resolved path must
// load into a populated source map so the TUI's source-view panel can
// render ca65 lines while the user steps through the ROM.
func TestDebug_AutoDetectAndLoadHelloBG(t *testing.T) {
	romPath := filepath.Join("..", "..", "roms", "demos", "hello-bg", "hello-bg.nes")
	dbg := symbols.SiblingDbg(romPath)
	if dbg == "" {
		t.Fatalf("SiblingDbg(%q) returned empty — hello-bg.dbg should exist alongside the .nes", romPath)
	}
	tbl, err := symbols.LoadDbg(dbg)
	if err != nil {
		t.Fatalf("LoadDbg(%s): %v", dbg, err)
	}
	sm, err := symbols.LoadSourceMap(dbg)
	if err != nil {
		t.Fatalf("LoadSourceMap(%s): %v", dbg, err)
	}
	if len(sm.PCToSrc) == 0 {
		t.Fatalf("source map is empty — ca65 needs `-g` and ld65 needs `--dbgfile` for line records to emit")
	}
	// The `reset:` label lands at $C000 (first byte of the CODE
	// segment); the .dbg should map at least one early PC to
	// `hello-bg.s`.
	loc, ok := sm.PCToSrc[0xC000]
	if !ok {
		// Try walking a few PCs forward — the line record may start
		// at the first real opcode (e.g. SEI) rather than the label
		// itself.
		for pc := uint16(0xC000); pc < 0xC020; pc++ {
			if l, found := sm.PCToSrc[pc]; found {
				loc = l
				ok = true
				break
			}
		}
	}
	if !ok {
		t.Fatalf("no source mapping for any PC in $C000-$C01F; .dbg line records missing or wrong-shape")
	}
	if filepath.Base(loc.File) != "hello-bg.s" {
		t.Errorf("source mapping points at %q; want hello-bg.s", loc.File)
	}
	if loc.Line == 0 {
		t.Errorf("source mapping has line=0; expected a real line number")
	}
	// The Files map must contain the loaded source — `(file
	// unavailable: hello-bg.s)` in the TUI source-view came from
	// this map being empty because the resolver couldn't find the
	// file on disk.
	lines, ok := sm.Files[loc.File]
	if !ok {
		t.Fatalf("sm.Files[%q] missing — source resolver couldn't load the file from disk", loc.File)
	}
	if len(lines) < 10 {
		t.Errorf("sm.Files[%q] has %d lines; expected the full source", loc.File, len(lines))
	}
	// `reset` is a documented exported symbol via ca65's `-g` debug
	// output; the symbol table should resolve it.
	if pc, ok := tbl.LookupName("reset"); !ok || pc != 0xC000 {
		t.Errorf("Table.LookupName(reset) = (%X, %v); want ($C000, true)", pc, ok)
	}
}
