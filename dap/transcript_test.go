package dap

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden regenerates the *.golden transcripts from current
// server behaviour: go test ./internal/dap/ -run TestTranscript -update
var updateGolden = flag.Bool("update", false, "regenerate DAP transcript goldens")

// Transcript golden tests (#169). Each scenario is a committed JSON
// array of client requests; the harness frames them, drives a real
// in-process dap.Server through Serve(), then decodes the framed
// reply stream into a flat message list and diffs it against a
// committed golden. Catches the regressions per-handler unit tests
// miss — capability drift, response/event ordering, request_seq
// echo, off-by-one sequencing.
//
// Decoded-message comparison (not raw Content-Length bytes) keeps
// the goldens diff-friendly + immune to header-formatting churn
// while still pinning every field the client sees.
//
// Scenarios are path-free (no launch of a real program) so the
// goldens are portable across machines + CI.
func TestTranscript(t *testing.T) {
	dir := filepath.Join("testdata", "dap-transcripts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read transcripts dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		scenario := name[:len(name)-len(".json")]
		t.Run(scenario, func(t *testing.T) {
			runTranscript(t, dir, scenario)
		})
	}
}

func runTranscript(t *testing.T, dir, scenario string) {
	reqData, err := os.ReadFile(filepath.Join(dir, scenario+".json"))
	if err != nil {
		t.Fatalf("read scenario: %v", err)
	}
	var requests []json.RawMessage
	if err := json.Unmarshal(reqData, &requests); err != nil {
		t.Fatalf("parse scenario: %v", err)
	}

	// Frame each request into the Content-Length wire stream.
	var in bytes.Buffer
	for _, r := range requests {
		if err := WriteMessage(&in, r); err != nil {
			t.Fatalf("frame request: %v", err)
		}
	}

	var out bytes.Buffer
	srv := NewServer(&in, &out)
	if err := srv.Serve(); err != nil {
		t.Fatalf("serve: %v", err)
	}

	// Decode the framed reply stream into a flat message list +
	// re-marshal each compactly so the golden is canonical JSON,
	// one message per line.
	var got bytes.Buffer
	r := bufio.NewReader(&out)
	for {
		body, err := ReadMessage(r)
		if err != nil {
			break // EOF
		}
		var canon bytes.Buffer
		if err := json.Compact(&canon, body); err != nil {
			t.Fatalf("compact reply: %v", err)
		}
		got.Write(canon.Bytes())
		got.WriteByte('\n')
	}

	goldenPath := filepath.Join(dir, scenario+".golden")
	if *updateGolden {
		if err := os.WriteFile(goldenPath, got.Bytes(), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden (run with -update to generate): %v", err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("transcript %s diverged from golden.\n--- got ---\n%s\n--- want ---\n%s",
			scenario, got.String(), string(want))
	}
}
