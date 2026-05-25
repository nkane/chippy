package dap

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// dapCompletionPool is the static list of identifiers chippy's expression
// grammar recognizes outside of user-loaded symbols. Registers + flag
// bits. Sorted for stable display in the editor's autocomplete popup.
var dapCompletionPool = []string{
	"A", "X", "Y", "P", "SP", "PC",
	"N", "V", "B", "D", "I", "Z", "C",
}

// handleCompletions powers the debug-console autocomplete popup. Walks
// back from the requested column to find the trailing identifier
// (letters / digits / underscore), then offers prefix matches from:
//
//   - dapCompletionPool (registers + flag bits)
//   - the loaded .dbg symbol table
//
// Returns items sorted alphabetically. Empty prefix yields the full
// register/flag list so the user gets discoverable suggestions on a
// blank prompt.
func (s *Server) handleCompletions(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	var args CompletionsArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad completions args: %v", err))
		return
	}
	prefix, start := identifierPrefix(args.Text, args.Column)

	matches := make([]CompletionItem, 0, 16)

	// Registers + flags.
	for _, name := range dapCompletionPool {
		if hasPrefixCaseFold(name, prefix) {
			matches = append(matches, CompletionItem{
				Label:  name,
				Text:   name,
				Type:   "variable",
				Start:  start,
				Length: len(prefix),
			})
		}
	}

	// User symbols from the loaded .dbg.
	if s.syms != nil {
		for _, name := range s.syms.NamesWithPrefix(prefix) {
			matches = append(matches, CompletionItem{
				Label:  name,
				Text:   name,
				Type:   "function",
				Start:  start,
				Length: len(prefix),
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return strings.ToLower(matches[i].Label) < strings.ToLower(matches[j].Label)
	})

	type body struct {
		Targets []CompletionItem `json:"targets"`
	}
	s.sendResponse(req, body{Targets: matches})
}

// identifierPrefix scans `text` backward from `column` (1-based, DAP
// convention) and returns the trailing identifier — the longest run of
// `[A-Za-z0-9_]` that ends at or just before the cursor. `start` is the
// 1-based column where that identifier begins (so the editor can
// replace the right span on accept).
//
// If the cursor isn't sitting on an identifier the function returns
// ("", column) — the empty prefix produces the full register/flag list
// on a blank input.
func identifierPrefix(text string, column int) (string, int) {
	// Convert 1-based column to a 0-based byte index, clamped.
	end := column - 1
	if end < 0 {
		end = 0
	}
	if end > len(text) {
		end = len(text)
	}
	i := end
	for i > 0 && isIdentByte(text[i-1]) {
		i--
	}
	return text[i:end], i + 1
}

func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') ||
		b == '_'
}

func hasPrefixCaseFold(s, prefix string) bool {
	if prefix == "" {
		return true
	}
	if len(prefix) > len(s) {
		return false
	}
	return strings.EqualFold(s[:len(prefix)], prefix)
}
