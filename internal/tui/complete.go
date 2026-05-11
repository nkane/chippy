package tui

import (
	"sort"
	"strings"

	"github.com/nkane/chippy/internal/symbols"
)

// defaultVerbs is the static set of prompt commands. Kept in sync with the
// switch in runCommand. Sorted at init so completePrompt can scan linearly.
var defaultVerbs = func() []string {
	v := []string{
		"goto", "g",
		"pc",
		"run",
		"watch", "w",
		"rmwatch", "unwatch",
		"clearwatch",
		"speed",
		"bp", "bpr", "bpw", "bprw",
		"rmbpr", "rmbpw", "rmbprw",
		"trace",
		"help", "?",
		"q", "quit",
	}
	sort.Strings(v)
	return v
}()

// verbsTakingAddr lists prompt verbs whose first argument is an address or
// a symbol name — these get tab-completion against the loaded symbol table.
var verbsTakingAddr = map[string]bool{
	"goto":    true,
	"g":       true,
	"pc":      true,
	"run":     true,
	"watch":   true,
	"w":       true,
	"rmwatch": true,
	"unwatch": true,
	"bp":      true,
	"bpr":     true,
	"bpw":     true,
	"bprw":    true,
	"rmbpr":   true,
	"rmbpw":   true,
	"rmbprw":  true,
}

// completePrompt extends buf toward the longest unambiguous completion.
// Returns (newBuf, ok) where ok=true means buf grew. Two regimes:
//
//   - no space yet -> complete the verb against defaultVerbs.
//   - first arg of an address-taking verb -> complete against syms.
//
// All other inputs return (buf, false) so a caller can tell when to ring
// the bell or just no-op.
func completePrompt(buf string, syms *symbols.Table) (string, bool) {
	spaceIdx := strings.Index(buf, " ")
	if spaceIdx < 0 {
		match := matchesWithPrefix(defaultVerbs, buf)
		if len(match) == 0 {
			return buf, false
		}
		// Unique match -> commit it and add a trailing space so the user
		// can keep typing the argument without re-tabbing. Handles both
		// "bpr" -> "bpr " (extension) and "bprw" -> "bprw " (already full).
		if len(match) == 1 {
			return match[0] + " ", true
		}
		lcp := longestCommonPrefix(match)
		if lcp == "" || lcp == buf {
			return buf, false
		}
		return lcp, true
	}
	verb := buf[:spaceIdx]
	if !verbsTakingAddr[verb] {
		return buf, false
	}
	prefix := buf[:spaceIdx+1]
	arg := buf[spaceIdx+1:]
	if syms == nil {
		return buf, false
	}
	names := syms.NamesWithPrefix(arg)
	lcp := longestCommonPrefix(names)
	if lcp == "" || lcp == arg {
		return buf, false
	}
	return prefix + lcp, true
}

func matchesWithPrefix(pool []string, prefix string) []string {
	out := make([]string, 0, 4)
	for _, s := range pool {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

func longestCommonPrefix(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	p := ss[0]
	for _, s := range ss[1:] {
		n := len(p)
		if len(s) < n {
			n = len(s)
		}
		i := 0
		for i < n && p[i] == s[i] {
			i++
		}
		p = p[:i]
		if p == "" {
			return ""
		}
	}
	return p
}
