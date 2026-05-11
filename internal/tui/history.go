package tui

import (
	"bufio"
	"os"
	"path/filepath"
)

// histCap caps the in-memory + on-disk history. 100 lines is plenty for
// interactive debugger sessions and keeps the file under a few KB.
const histCap = 100

// loadHistory reads up to histCap most-recent lines from path. Missing or
// unreadable files yield a nil slice — never an error — so the prompt is
// usable on first run.
func loadHistory(path string) []string {
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
	s.Buffer(make([]byte, 0, 4096), 64*1024)
	for s.Scan() {
		line := s.Text()
		if line != "" {
			out = append(out, line)
		}
	}
	if len(out) > histCap {
		out = out[len(out)-histCap:]
	}
	return out
}

// saveHistory writes history to path, creating the parent directory as
// needed. Failures are silently swallowed at the call site — losing
// history is a quality-of-life regression, not a correctness bug.
func saveHistory(path string, history []string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, line := range history {
		if _, err := w.WriteString(line); err != nil {
			return err
		}
		if err := w.WriteByte('\n'); err != nil {
			return err
		}
	}
	return w.Flush()
}

// DefaultHistoryPath returns ~/.chippy/history. Unlike state files, history
// isn't per-ROM — debugger muscle-memory commands (`:bp main`, `:speed 1000`)
// carry over across projects.
func DefaultHistoryPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".chippy", "history")
}
