package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/nkane/chippy/internal/symbols"
)

func TestComplete_VerbExactlyOne(t *testing.T) {
	got, ok := completePrompt("clearw", nil)
	if !ok {
		t.Fatalf("expected match")
	}
	if got != "clearwatch " {
		t.Fatalf("want %q (with trailing space), got %q", "clearwatch ", got)
	}
}

func TestComplete_VerbAmbiguousNoExtension(t *testing.T) {
	if _, ok := completePrompt("bp", nil); ok {
		t.Fatalf("ambiguous prefix shouldn't complete: bp matches bp/bpr/bpw/bprw")
	}
}

func TestComplete_VerbUniqueAddsSpace(t *testing.T) {
	got, ok := completePrompt("bprw", nil)
	if !ok {
		t.Fatalf("bprw unique -> should add trailing space")
	}
	if got != "bprw " {
		t.Fatalf("want %q, got %q", "bprw ", got)
	}
}

func TestComplete_VerbNoMatch(t *testing.T) {
	if _, ok := completePrompt("zzz", nil); ok {
		t.Fatalf("no-match prefix should not complete")
	}
}

func TestComplete_SymbolUnique(t *testing.T) {
	tbl := buildSyms(t, "main", 0x8000)
	got, ok := completePrompt("bp m", tbl)
	if !ok {
		t.Fatalf("expected completion of `bp m` -> `bp main`")
	}
	if got != "bp main" {
		t.Fatalf("want %q, got %q", "bp main", got)
	}
}

func TestComplete_SymbolAmbiguous(t *testing.T) {
	tbl := buildSyms(t, "main", 0x8000, "main_loop", 0x8010, "main_end", 0x8030)
	got, ok := completePrompt("bp m", tbl)
	if !ok {
		t.Fatalf("expected partial completion to `main`")
	}
	if got != "bp main" {
		t.Fatalf("want partial %q, got %q", "bp main", got)
	}
	if _, ok := completePrompt("bp main", tbl); ok {
		t.Fatalf("further tab when lcp == input should not extend")
	}
}

func TestComplete_VerbThatDoesNotTakeAddr(t *testing.T) {
	tbl := buildSyms(t, "main", 0x8000)
	if _, ok := completePrompt("clearwatch m", tbl); ok {
		t.Fatalf("non-addr verb should not get symbol completion")
	}
}

// :trace argument completion: unique sub-keyword completes + trailing space.
func TestComplete_TraceSubcommand(t *testing.T) {
	// "o" is ambiguous between "on" and "off" — no extension (lcp == trailing).
	if got, ok := completePrompt("trace o", nil); ok {
		t.Fatalf("trace o is ambiguous; should not extend; got %q,%v", got, ok)
	}
	// "of" uniquely identifies "off".
	got, ok := completePrompt("trace of", nil)
	if !ok || got != "trace off " {
		t.Fatalf("trace of -> want %q,true; got %q,%v", "trace off ", got, ok)
	}
	// "on" is already full — commit + trailing space.
	got, ok = completePrompt("trace on", nil)
	if !ok || got != "trace on " {
		t.Fatalf("trace on -> want %q,true; got %q,%v", "trace on ", got, ok)
	}
}

// :speed argument completion uses the speedSuggestions pool.
func TestComplete_SpeedSuggestions(t *testing.T) {
	got, ok := completePrompt("speed 6", nil)
	if !ok || got != "speed 60 " {
		t.Fatalf("speed 6 -> want %q,true; got %q,%v", "speed 60 ", got, ok)
	}
}

// :bp X <modifier>: at arg position >= 2 of a bp verb, complete against
// the modifier pool.
func TestComplete_BPModifier(t *testing.T) {
	got, ok := completePrompt("bp main on", nil)
	if !ok || got != "bp main once " {
		t.Fatalf("bp main on -> want %q,true; got %q,%v", "bp main once ", got, ok)
	}
	got, ok = completePrompt("bp $8000 h", nil)
	if !ok || got != "bp $8000 hits " {
		t.Fatalf("bp $8000 h -> want %q,true; got %q,%v", "bp $8000 hits ", got, ok)
	}
}

// :textsave is in the verb pool so first-char tab still finds it.
func TestComplete_TextsaveVerb(t *testing.T) {
	got, ok := completePrompt("textsa", nil)
	if !ok || got != "textsave " {
		t.Fatalf("textsa -> want %q,true; got %q,%v", "textsave ", got, ok)
	}
}

// First-arg symbol completion still works for bp + similar verbs. The
// new arg-position routing must not break that path.
func TestComplete_SymbolCompletionStillWorksAtArg1(t *testing.T) {
	tbl := buildSyms(t, "main", 0x8000)
	got, ok := completePrompt("goto m", tbl)
	if !ok || got != "goto main" {
		t.Fatalf("goto m -> want %q,true; got %q,%v", "goto main", got, ok)
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"foo"}, "foo"},
		{[]string{"foo", "foobar"}, "foo"},
		{[]string{"foo", "fab"}, "f"},
		{[]string{"foo", "bar"}, ""},
		{[]string{"aaa", "aab", "aac"}, "aa"},
	}
	for _, c := range cases {
		if got := longestCommonPrefix(c.in); got != c.want {
			t.Errorf("lcp(%v): want %q, got %q", c.in, c.want, got)
		}
	}
}

// buildSyms writes a minimal cc65-style .dbg file with one `sym` record
// per (name, addr) pair, then loads it through the real parser so the test
// exercises the public Table type without relying on internal fields.
func buildSyms(t *testing.T, nameAddr ...interface{}) *symbols.Table {
	t.Helper()
	if len(nameAddr)%2 != 0 {
		t.Fatalf("buildSyms: odd number of name/addr args")
	}
	path := filepath.Join(t.TempDir(), "x.dbg")
	var body []byte
	for i := 0; i < len(nameAddr); i += 2 {
		name := nameAddr[i].(string)
		addr := nameAddr[i+1].(int)
		body = append(body, []byte(fmt.Sprintf("sym\tname=\"%s\",val=0x%04X\n", name, addr))...)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write dbg: %v", err)
	}
	tbl, err := symbols.LoadDbg(path)
	if err != nil {
		t.Fatalf("LoadDbg: %v", err)
	}
	return tbl
}
