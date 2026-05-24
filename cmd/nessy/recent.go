//go:build nessy

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

// recentMax caps the recent-ROM list. Five matches the typical
// "last few games I was playing" window without scrolling.
const recentMax = 5

// recentPath returns the disk location of the recent-ROM list.
// Empty when $HOME isn't resolvable.
func recentPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".nessy", "recent")
}

// loadRecent reads up to recentMax absolute paths from the recent
// list, most-recent first. Missing file → empty slice, no error.
func loadRecent() []string {
	path := recentPath()
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) == recentMax {
			break
		}
	}
	return out
}

// recordRecent prepends romPath to the recent list, dedupes, and
// truncates to recentMax. Best-effort: persistence errors print
// to stderr but don't abort the run.
func recordRecent(romPath string) {
	abs, err := filepath.Abs(romPath)
	if err != nil {
		abs = romPath
	}
	prior := loadRecent()
	out := []string{abs}
	for _, p := range prior {
		if p == abs {
			continue
		}
		out = append(out, p)
		if len(out) == recentMax {
			break
		}
	}
	path := recentPath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "nessy: mkdir recent:", err)
		return
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "nessy: write recent:", err)
		return
	}
	defer f.Close()
	for _, p := range out {
		fmt.Fprintln(f, p)
	}
}

// parseRecentSlot interprets a CLI positional as a recent-list
// index when it's a pure 1-2 digit integer; anything else (paths,
// dots, slashes) returns false so main treats it as a path.
func parseRecentSlot(s string) (int, bool) {
	if len(s) == 0 || len(s) > 2 {
		return 0, false
	}
	slot := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		slot = slot*10 + int(r-'0')
	}
	if slot == 0 {
		return 0, false
	}
	return slot, true
}

// printRecent dumps the numbered recent list to stderr. Used by
// the "no args" startup path.
func printRecent(list []string) {
	fmt.Fprintln(os.Stderr, "nessy: recent ROMs (run `nessy N` to open one):")
	if len(list) == 0 {
		fmt.Fprintln(os.Stderr, "  (no recent ROMs)")
		return
	}
	for i, p := range list {
		fmt.Fprintf(os.Stderr, "  %d  %s\n", i+1, p)
	}
}
