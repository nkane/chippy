package dap

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// handleLoadedSources returns one Source entry per file the loaded .dbg
// references. Lights up the editor's Loaded Scripts pane so the user can
// open files chippy knows about even when they aren't in the workspace.
func (s *Server) handleLoadedSources(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	type body struct {
		Sources []Source `json:"sources"`
	}
	if s.srcMap == nil {
		s.sendResponse(req, body{Sources: []Source{}})
		return
	}
	out := make([]Source, 0, len(s.srcMap.Files))
	for path := range s.srcMap.Files {
		out = append(out, Source{
			Name: filepath.Base(path),
			Path: path,
		})
	}
	// Stable ordering so the editor displays the same list each refresh.
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	s.sendResponse(req, body{Sources: out})
}

// handleSource returns the cached file contents for a previously-listed
// source. Used by editors that want to display a source file chippy
// loaded (via .dbg) but the user doesn't have open in their workspace.
func (s *Server) handleSource(req Request) {
	if s.cpu == nil {
		s.sendErrorResponse(req, "no debuggee — send launch first")
		return
	}
	if s.srcMap == nil {
		s.sendErrorResponse(req, "no source map loaded")
		return
	}
	var args SourceArguments
	if err := json.Unmarshal(req.Arguments, &args); err != nil {
		s.sendErrorResponse(req, fmt.Sprintf("bad source args: %v", err))
		return
	}
	want := canonicalSourcePath(args.Source)
	for path, lines := range s.srcMap.Files {
		if path == want || filepath.Base(path) == filepath.Base(want) {
			type body struct {
				Content  string `json:"content"`
				MimeType string `json:"mimeType,omitempty"`
			}
			s.sendResponse(req, body{
				Content:  strings.Join(lines, "\n"),
				MimeType: "text/plain",
			})
			return
		}
	}
	s.sendErrorResponse(req, fmt.Sprintf("source not loaded: %s", want))
}
