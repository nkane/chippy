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
