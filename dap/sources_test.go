package dap

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nkane/chippy/symbols"
)

func srcMapWithFiles(files map[string][]string) *symbols.SourceMap {
	return &symbols.SourceMap{
		PCToSrc: map[uint16]symbols.SrcLoc{},
		Files:   files,
	}
}

func TestSources_LoadedSourcesEmpty(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	// s.srcMap nil — no .dbg loaded.

	s.handleLoadedSources(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "loadedSources",
	})

	body := out.String()
	if !strings.Contains(body, `"sources":[]`) {
		t.Fatalf("nil srcMap should yield empty sources, got: %s", body)
	}
}

func TestSources_LoadedSourcesLists(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.srcMap = srcMapWithFiles(map[string][]string{
		"main.s":  {"line1", "line2"},
		"util.s":  {"x"},
		"start.s": {"y"},
	})

	s.handleLoadedSources(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "loadedSources",
	})

	body := out.String()
	for _, want := range []string{`"name":"main.s"`, `"name":"util.s"`, `"name":"start.s"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
		}
	}
	// Sorted by path.
	if !strings.Contains(body, `"path":"main.s"`) {
		t.Fatalf("expected path field populated: %s", body)
	}
}

func TestSources_SourceReturnsContent(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.srcMap = srcMapWithFiles(map[string][]string{
		"main.s": {"  lda #$42", "  rts"},
	})

	s.handleSource(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "source",
		Arguments:       json.RawMessage(`{"source":{"name":"main.s","path":"main.s"}}`),
	})

	body := out.String()
	if !strings.Contains(body, `"content":"  lda #$42\n  rts"`) {
		t.Fatalf("expected joined content, got: %s", body)
	}
}

func TestSources_SourceBasenameFallback(t *testing.T) {
	// .dbg recorded a bare filename; client passes an absolute path.
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.srcMap = srcMapWithFiles(map[string][]string{
		"main.s": {"  lda"},
	})

	s.handleSource(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "source",
		Arguments:       json.RawMessage(`{"source":{"name":"main.s","path":"/abs/project/main.s"}}`),
	})

	if !strings.Contains(out.String(), `"content":"  lda"`) {
		t.Fatalf("basename match should succeed: %s", out.String())
	}
}

func TestSources_SourceUnknown(t *testing.T) {
	s, _, out := newStoppedServer(t, []byte{0xEA})
	s.srcMap = srcMapWithFiles(map[string][]string{
		"main.s": {"x"},
	})

	s.handleSource(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "source",
		Arguments:       json.RawMessage(`{"source":{"name":"missing.s","path":"missing.s"}}`),
	})

	if !strings.Contains(out.String(), `"success":false`) {
		t.Fatalf("unknown source should error, got: %s", out.String())
	}
}

func TestSources_CapabilityAdvertised(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.handleInitialize(Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "initialize",
		Arguments:       json.RawMessage(`{"adapterID":"chippy"}`),
	})
	if !strings.Contains(out.String(), `"supportsLoadedSourcesRequest":true`) {
		t.Fatalf("initialize should advertise supportsLoadedSourcesRequest:true, got: %s", out.String())
	}
}
