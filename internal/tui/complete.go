package tui

import (
	"sort"
	"strings"

	"github.com/nkane/chippy/symbols"
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
		"syms", "symbols",
		"trace",
		"textsave",
		"theme",
		"dap",
		"help", "?",
		"q", "quit",
	}
	sort.Strings(v)
	return v
}()

// traceSubcommands is the first-arg completion pool for `:trace`. A
// bare path is accepted at runtime but isn't completed.
var traceSubcommands = []string{"on", "off"}

// speedSuggestions are common targets for `:speed`. 0 = unthrottled; the
// rest cover sub-Hz to 1 kHz so the completer offers ballparks.
var speedSuggestions = []string{"0", "1", "10", "60", "120", "1000"}

// bpModifiers covers the trailing `:bp X <modifier>` arg position. Order
// matches the help-modal listing (Page 3 → Breakpoints).
var bpModifiers = []string{"once", "hits", "if", "log"}

var bpVerbs = map[string]bool{
	"bp": true, "bpr": true, "bpw": true, "bprw": true,
}

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

	// Walk arg positions so we can route the trailing word to the right
	// completion pool. words[0] is verb; words[1] is first arg, etc.
	// trailing is the word currently being typed (may be empty when the
	// buffer ends in a space).
	tail := buf[spaceIdx+1:]
	words := strings.Fields(tail)
	trailing := ""
	if !strings.HasSuffix(buf, " ") && len(words) > 0 {
		trailing = words[len(words)-1]
		words = words[:len(words)-1]
	}
	argPos := len(words) + 1 // 1-indexed position of the trailing word

	switch verb {
	case "trace":
		if argPos == 1 {
			return completeAgainstPool(buf, trailing, traceSubcommands)
		}
	case "speed":
		if argPos == 1 {
			return completeAgainstPool(buf, trailing, speedSuggestions)
		}
	case "theme":
		if argPos == 1 {
			return completeAgainstPool(buf, trailing, AvailableThemes())
		}
	}
	// :bp / :bpr / :bpw / :bprw: address at pos 1 (symbol completion);
	// modifier at pos >= 2.
	if bpVerbs[verb] && argPos >= 2 {
		return completeAgainstPool(buf, trailing, bpModifiers)
	}

	if !verbsTakingAddr[verb] {
		return buf, false
	}
	if argPos != 1 {
		// Address-taking verbs only complete their first argument.
		return buf, false
	}
	if syms == nil {
		return buf, false
	}
	names := syms.NamesWithPrefix(trailing)
	lcp := longestCommonPrefix(names)
	if lcp == "" || lcp == trailing {
		return buf, false
	}
	// Rebuild buf with the trailing word replaced.
	prefix := buf[:len(buf)-len(trailing)]
	return prefix + lcp, true
}

// completeAgainstPool extends the trailing word to the longest unique
// prefix from pool. Mirrors the verb-completion logic at top of
// completePrompt: unique match commits + trailing space; multiple
// matches collapse to LCP; no match returns unchanged.
func completeAgainstPool(buf, trailing string, pool []string) (string, bool) {
	match := matchesWithPrefix(pool, trailing)
	if len(match) == 0 {
		return buf, false
	}
	prefix := buf[:len(buf)-len(trailing)]
	if len(match) == 1 {
		return prefix + match[0] + " ", true
	}
	lcp := longestCommonPrefix(match)
	if lcp == "" || lcp == trailing {
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
