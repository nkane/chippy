package symbols

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDbg drops a minimal .dbg with the given lines into a temp dir and
// returns its path.
func writeDbg(t *testing.T, lines string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.dbg")
	if err := os.WriteFile(p, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDbg_SymSize(t *testing.T) {
	dbg := writeDbg(t, ""+
		"sym\tname=\"_grid\",addrsize=absolute,size=32,scope=0,val=0x0400,seg=2,type=lab\n"+
		"sym\tname=\"_flag\",addrsize=absolute,scope=0,val=0x0500,seg=2,type=lab\n")
	tab, err := LoadDbg(dbg)
	if err != nil {
		t.Fatal(err)
	}
	if got := tab.Size(0x0400); got != 32 {
		t.Errorf("Size($0400) = %d; want 32", got)
	}
	// No size= field recorded -> 0 ("unknown"), not an error.
	if got := tab.Size(0x0500); got != 0 {
		t.Errorf("Size($0500) = %d; want 0 (unknown)", got)
	}
	// Unknown address -> 0.
	if got := tab.Size(0x9999); got != 0 {
		t.Errorf("Size($9999) = %d; want 0", got)
	}
}

func TestTable_Size_NilSafe(t *testing.T) {
	var tab *Table
	if got := tab.Size(0x0400); got != 0 {
		t.Errorf("nil Table Size = %d; want 0", got)
	}
}
