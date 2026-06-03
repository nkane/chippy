// Package symbols parses cc65 .dbg debug-info files and exposes
// address->name lookups for use in the disassembler.
//
// cc65 .dbg files are line-oriented text. Each line is:
//
//	tag\tkey="val",key=N,...
//
// We only care about `sym` records that have a `val` (address) and a `name`,
// and we strip the surrounding quotes and any leading underscore that the
// C compiler emits.
package symbols

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Table maps addresses to the symbol(s) defined there.
type Table struct {
	byAddr   map[uint16]string
	byName   map[string]uint16
	sizeByAd map[uint16]int // sym `size=` field, when cc65 emits one
}

func New() *Table {
	return &Table{
		byAddr:   map[uint16]string{},
		byName:   map[string]uint16{},
		sizeByAd: map[uint16]int{},
	}
}

// Size returns the byte extent cc65 recorded for the symbol at addr, or 0
// when unknown. cc65 only emits `size=` on some `sym` records (notably
// code labels); data/BSS globals frequently lack it, so callers must treat
// 0 as "size unavailable", not "zero-length".
func (t *Table) Size(addr uint16) int {
	if t == nil {
		return 0
	}
	return t.sizeByAd[addr]
}

// Lookup returns the symbol at addr, or "" if none.
func (t *Table) Lookup(addr uint16) string {
	if t == nil {
		return ""
	}
	return t.byAddr[addr]
}

// LookupName returns the address of a named symbol, if any.
func (t *Table) LookupName(name string) (uint16, bool) {
	if t == nil {
		return 0, false
	}
	a, ok := t.byName[name]
	return a, ok
}

// Has reports whether any symbols are loaded.
func (t *Table) Has() bool { return t != nil && len(t.byAddr) > 0 }

// NamesWithPrefix returns all symbol names whose string starts with prefix,
// sorted lexicographically. Used for tab-completion in the TUI prompt.
func (t *Table) NamesWithPrefix(prefix string) []string {
	if t == nil {
		return nil
	}
	out := make([]string, 0, 16)
	for name := range t.byName {
		if strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// LoadDbg parses a cc65 .dbg file and returns a populated Table.
func LoadDbg(path string) (*Table, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	t := New()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scan.Scan() {
		line := scan.Text()
		// Format: tag<TAB>k=v,k=v ... but some versions use spaces. Normalize.
		var tag, rest string
		if i := strings.IndexAny(line, "\t "); i > 0 {
			tag = line[:i]
			rest = strings.TrimSpace(line[i+1:])
		} else {
			continue
		}
		if tag != "sym" {
			continue
		}
		fields := parseKV(rest)
		nameRaw, ok := fields["name"]
		if !ok {
			continue
		}
		valRaw, ok := fields["val"]
		if !ok {
			continue
		}
		name := strings.Trim(nameRaw, `"`)
		// cc65 prefixes C symbols with '_'; strip for readability.
		name = strings.TrimPrefix(name, "_")
		if name == "" {
			continue
		}
		addr, err := parseNum(valRaw)
		if err != nil {
			continue
		}
		if addr > 0xFFFF {
			continue
		}
		// First definition wins; later equates won't clobber a real label.
		if _, exists := t.byAddr[uint16(addr)]; !exists {
			t.byAddr[uint16(addr)] = name
		}
		if _, exists := t.byName[name]; !exists {
			t.byName[name] = uint16(addr)
		}
		if sz, ok := fields["size"]; ok {
			if n, err := parseNum(sz); err == nil && n > 0 {
				t.sizeByAd[uint16(addr)] = int(n)
			}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return t, nil
}

// parseKV splits "k=v,k=v" into a map. Handles quoted values with embedded commas.
func parseKV(s string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(s) {
		// key
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(s[i : i+eq])
		i += eq + 1
		// value: quoted or until next unquoted comma
		var val string
		if i < len(s) && s[i] == '"' {
			i++
			end := strings.IndexByte(s[i:], '"')
			if end < 0 {
				break
			}
			val = s[i : i+end]
			i += end + 1
			// skip optional trailing comma
			if i < len(s) && s[i] == ',' {
				i++
			}
		} else {
			end := strings.IndexByte(s[i:], ',')
			if end < 0 {
				val = strings.TrimSpace(s[i:])
				i = len(s)
			} else {
				val = strings.TrimSpace(s[i : i+end])
				i += end + 1
			}
		}
		out[key] = val
	}
	return out
}

// parseNum accepts cc65 numeric forms: 0xABCD, $ABCD, decimal.
func parseNum(s string) (uint32, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		v, err := strconv.ParseUint(s[2:], 16, 32)
		return uint32(v), err
	case strings.HasPrefix(s, "$"):
		v, err := strconv.ParseUint(s[1:], 16, 32)
		return uint32(v), err
	default:
		v, err := strconv.ParseUint(s, 10, 32)
		return uint32(v), err
	}
}

// SiblingDbg returns the conventional .dbg path for a given binary path
// (e.g. "out.bin" -> "out.dbg"). Returns "" if no such file exists.
func SiblingDbg(binPath string) string {
	if binPath == "" {
		return ""
	}
	candidates := []string{
		strings.TrimSuffix(binPath, extOf(binPath)) + ".dbg",
		binPath + ".dbg",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func extOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '.' {
			return p[i:]
		}
		if p[i] == '/' {
			break
		}
	}
	return ""
}

// Format returns "name" or "name+N" for an address falling inside a known symbol's range.
// Without ranges we just return the exact-address name.
func (t *Table) Format(addr uint16) string {
	if t == nil {
		return ""
	}
	if name := t.byAddr[addr]; name != "" {
		return name
	}
	return ""
}

// Debug helper: stringified table.
func (t *Table) String() string {
	if t == nil {
		return "<no symbols>"
	}
	var b strings.Builder
	for a, n := range t.byAddr {
		fmt.Fprintf(&b, "$%04X %s\n", a, n)
	}
	return b.String()
}

// SrcLoc is a (file, line) source location.
type SrcLoc struct {
	File string
	Line int // 1-indexed
}

// SourceMap is the result of parsing `file`, `line`, and `span` records from a
// cc65 .dbg file. It maps every PC in the program to its originating source
// location, and provides the file contents for rendering.
type SourceMap struct {
	PCToSrc map[uint16]SrcLoc
	Files   map[string][]string // filename -> source lines (1-indexed via [i-1])
	// DataRanges lists [start, end) byte ranges flagged as non-code by cc65
	// (segments with type=rw, i.e. RAM/BSS/DATA). Disassemblers can use this
	// to render `.byte $XX` instead of bogus mnemonics for these regions.
	DataRanges []Range
}

// Range is a half-open [Start, End) byte range.
type Range struct {
	Start, End uint16
}

// IsData reports whether addr falls inside any DataRange.
func (sm *SourceMap) IsData(addr uint16) bool {
	if sm == nil {
		return false
	}
	// Code mapping always wins: an address with a source line is code.
	if _, ok := sm.PCToSrc[addr]; ok {
		return false
	}
	for _, r := range sm.DataRanges {
		if addr >= r.Start && addr < r.End {
			return true
		}
	}
	return false
}

// sourceFileCandidates lists the on-disk paths to try when locating
// a source file recorded in a `.dbg`. Order is intentional: absolute
// path → dbg-relative → basename-in-dbgdir → parent-of-dbgdir-relative.
// First existing readable file wins; missing ones fall through.
func sourceFileCandidates(dbgDir, recorded string) []string {
	if filepath.IsAbs(recorded) {
		return []string{recorded}
	}
	parent := filepath.Dir(dbgDir)
	return []string{
		filepath.Join(dbgDir, recorded),
		filepath.Join(dbgDir, filepath.Base(recorded)),
		filepath.Join(parent, recorded),
	}
}

// LoadSourceMap parses a cc65 .dbg file and returns a SourceMap.
//
// cc65 emits records like:
//
//	file id=N,name="path/to/foo.s",size=NNN,mtime=...
//	line id=N,file=F,line=L,span=S+S+...
//	span id=N,seg=N,start=N,size=N
//	seg  id=N,name=...,start=$XXXX,size=$NN
//
// We resolve span start = seg.start + span.start, then map
// [start, start+size) -> (file, line).
func LoadSourceMap(dbgPath string) (*SourceMap, error) {
	f, err := os.Open(dbgPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	type segR struct {
		start uint32
		size  uint32
		typ   string // cc65 segment type: "rw", "ro", etc.
	}
	type spanR struct {
		seg   int
		start uint32
		size  uint32
	}
	type fileR struct {
		name string
	}
	type lineR struct {
		file  int
		line  int
		spans []int
	}

	segs := map[int]segR{}
	spans := map[int]spanR{}
	files := map[int]fileR{}
	var lines []lineR

	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		raw := scan.Text()
		var tag, rest string
		if i := strings.IndexAny(raw, "\t "); i > 0 {
			tag = raw[:i]
			rest = strings.TrimSpace(raw[i+1:])
		} else {
			continue
		}
		kv := parseKV(rest)
		switch tag {
		case "seg":
			id, err := strconv.Atoi(kv["id"])
			if err != nil {
				continue
			}
			start, err := parseNum(kv["start"])
			if err != nil {
				continue
			}
			size, _ := parseNum(kv["size"])
			typ := strings.Trim(kv["type"], `"`)
			segs[id] = segR{start: start, size: size, typ: typ}
		case "span":
			id, err := strconv.Atoi(kv["id"])
			if err != nil {
				continue
			}
			seg, _ := strconv.Atoi(kv["seg"])
			start, _ := parseNum(kv["start"])
			size, _ := parseNum(kv["size"])
			spans[id] = spanR{seg: seg, start: start, size: size}
		case "file":
			id, err := strconv.Atoi(kv["id"])
			if err != nil {
				continue
			}
			name := strings.Trim(kv["name"], `"`)
			files[id] = fileR{name: name}
		case "line":
			file, _ := strconv.Atoi(kv["file"])
			ln, _ := strconv.Atoi(kv["line"])
			spanList := kv["span"]
			var ids []int
			for _, s := range strings.Split(spanList, "+") {
				if s == "" {
					continue
				}
				id, err := strconv.Atoi(s)
				if err != nil {
					continue
				}
				ids = append(ids, id)
			}
			if ln > 0 && len(ids) > 0 {
				lines = append(lines, lineR{file: file, line: ln, spans: ids})
			}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}

	// Two-pass build: cc65 emits records for both the .s intermediate
	// and the original .c source at the same PC. We want the .c file
	// to win so users see C source while stepping. Pass 1 fills the map
	// from non-.s sources (.c, .h, .inc, etc.); pass 2 backfills any
	// PCs still uncovered using .s records.
	isPreferredSource := func(name string) bool {
		lower := strings.ToLower(name)
		return !strings.HasSuffix(lower, ".s") &&
			!strings.HasSuffix(lower, ".asm") &&
			!strings.HasSuffix(lower, ".mac") &&
			!strings.HasSuffix(lower, ".inc")
	}
	pcMap := map[uint16]SrcLoc{}
	for _, preferred := range []bool{true, false} {
		for _, ln := range lines {
			fr, ok := files[ln.file]
			if !ok {
				continue
			}
			if isPreferredSource(fr.name) != preferred {
				continue
			}
			for _, sid := range ln.spans {
				sp, ok := spans[sid]
				if !ok {
					continue
				}
				seg, ok := segs[sp.seg]
				if !ok {
					continue
				}
				start := seg.start + sp.start
				for off := uint32(0); off < sp.size; off++ {
					addr := start + off
					if addr > 0xFFFF {
						break
					}
					if _, exists := pcMap[uint16(addr)]; !exists {
						pcMap[uint16(addr)] = SrcLoc{File: fr.name, Line: ln.line}
					}
				}
			}
		}
	}

	// Read source files. ca65 / ld65 records the path it was invoked
	// with, so the on-disk location depends on the build's working
	// directory. Try a few candidates to be robust to common build
	// layouts:
	//   1. Absolute path as recorded.
	//   2. Relative to the .dbg's directory.
	//   3. Just the basename, in the .dbg's directory (fixes the
	//      "ld65 invoked from one-up dir" pattern where the .dbg ends
	//      up with `subdir/file.s` and the loader's caller looked one
	//      level too deep).
	//   4. Walking up one directory from the .dbg.
	dbgDir := filepath.Dir(dbgPath)
	sources := map[string][]string{}
	seen := map[string]bool{}
	for _, fr := range files {
		if seen[fr.name] {
			continue
		}
		seen[fr.name] = true
		var data []byte
		var err error
		for _, candidate := range sourceFileCandidates(dbgDir, fr.name) {
			data, err = os.ReadFile(candidate)
			if err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		sources[fr.name] = strings.Split(string(data), "\n")
	}

	var dataRanges []Range
	for _, s := range segs {
		if s.size == 0 || s.typ != "rw" {
			continue
		}
		end := s.start + s.size
		if end > 0x10000 {
			end = 0x10000
		}
		dataRanges = append(dataRanges, Range{Start: uint16(s.start), End: uint16(end & 0xFFFF)})
	}

	return &SourceMap{PCToSrc: pcMap, Files: sources, DataRanges: dataRanges}, nil
}
