package dap

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestMem_DisassembleForward(t *testing.T) {
	// LDA #$42 (2B) ; NOP (1B) ; JMP $8000 (3B)
	s, _, out := newStoppedServer(t, []byte{0xA9, 0x42, 0xEA, 0x4C, 0x00, 0x80})

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "disassemble",
		Arguments:       json.RawMessage(`{"memoryReference":"$8000","instructionCount":3}`),
	}
	s.handleDisassemble(req)

	body := out.String()
	for _, want := range []string{
		`"address":"$8000"`,
		`"instructionBytes":"A9 42"`,
		`"address":"$8002"`,
		`"instructionBytes":"EA"`,
		`"address":"$8003"`,
		`"instructionBytes":"4C 00 80"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
		}
	}
}

func TestMem_ReadMemoryRoundTrip(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.ram.Write(0x0100, 0xDE)
	s.ram.Write(0x0101, 0xAD)
	s.ram.Write(0x0102, 0xBE)
	s.ram.Write(0x0103, 0xEF)

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "readMemory",
		Arguments:       json.RawMessage(`{"memoryReference":"$0100","count":4}`),
	}
	s.handleReadMemory(req)

	body := out.String()
	if !strings.Contains(body, `"address":"$0100"`) {
		t.Fatalf("missing address: %s", body)
	}
	want := base64.StdEncoding.EncodeToString([]byte{0xDE, 0xAD, 0xBE, 0xEF})
	if !strings.Contains(body, `"data":"`+want+`"`) {
		t.Fatalf("expected data=%q in body:\n%s", want, body)
	}
}

func TestMem_ReadMemoryClampsAtEnd(t *testing.T) {
	s, _, out := newStoppedServer(t, nil)
	s.ram.Write(0xFFFF, 0x99)

	// Ask for 10 bytes starting at $FFFE — only 2 available.
	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "readMemory",
		Arguments:       json.RawMessage(`{"memoryReference":"$FFFE","count":10}`),
	}
	s.handleReadMemory(req)

	body := out.String()
	// Decoded length should be 2.
	// Find the data field.
	if !strings.Contains(body, `"address":"$FFFE"`) {
		t.Fatalf("missing address: %s", body)
	}
	// Cheap check: base64("xx99") is 4 chars long base64 ("ABC=") for 2 raw bytes
	// — exact compare needs parsing. Confirm we DIDN'T pad to 10 bytes by
	// counting raw bytes in the decoded data.
	var parsed struct {
		Body struct {
			Data string `json:"data"`
		} `json:"body"`
	}
	for _, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, `"command":"readMemory"`) {
			continue
		}
		if err := json.Unmarshal([]byte(line), &parsed); err == nil {
			break
		}
	}
	raw, err := base64.StdEncoding.DecodeString(parsed.Body.Data)
	if err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("clamp: want 2 bytes ($FFFE..$FFFF), got %d", len(raw))
	}
	if raw[1] != 0x99 {
		t.Fatalf("expected $FFFF=$99, got $%02X", raw[1])
	}
}

func TestMem_WriteMemoryThenRead(t *testing.T) {
	s, _, _ := newStoppedServer(t, nil)

	payload := []byte{0x12, 0x34, 0x56, 0x78}
	enc := base64.StdEncoding.EncodeToString(payload)

	writeReq := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "writeMemory",
		Arguments:       json.RawMessage(`{"memoryReference":"$0200","data":"` + enc + `"}`),
	}
	s.handleWriteMemory(writeReq)

	for i, want := range payload {
		got := s.ram.Read(uint16(0x0200 + i))
		if got != want {
			t.Fatalf("RAM[$%04X]: want $%02X, got $%02X", 0x0200+i, want, got)
		}
	}
}

func TestMem_DisassembleBackwardContext(t *testing.T) {
	// Layout: $8000 A9 42 (LDA #$42, 2B) ; $8002 EA (NOP, 1B) ; $8003 EA (NOP, 1B) ; $8004 4C 00 80 (JMP $8000, 3B)
	// Asking from $8004 with instructionOffset=-3 should produce $8000, $8002, $8003 as pre-context, then $8004 as the reference.
	s, _, out := newStoppedServer(t, []byte{0xA9, 0x42, 0xEA, 0xEA, 0x4C, 0x00, 0x80})

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "disassemble",
		Arguments:       json.RawMessage(`{"memoryReference":"$8004","instructionOffset":-3,"instructionCount":4}`),
	}
	s.handleDisassemble(req)

	body := out.String()
	for _, want := range []string{
		`"address":"$8000"`,
		`"address":"$8002"`,
		`"address":"$8003"`,
		`"address":"$8004"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s in body:\n%s", want, body)
		}
	}
	// Confirm address ordering: $8000 must appear before $8004 in the output.
	idx8000 := strings.Index(body, `"address":"$8000"`)
	idx8004 := strings.Index(body, `"address":"$8004"`)
	if idx8000 >= idx8004 {
		t.Fatalf("pre-context $8000 should appear before $8004 in body:\n%s", body)
	}
}

func TestMem_DisassembleUsesVariantTable(t *testing.T) {
	// CMOS BRA = $80 rel. NMOS legacy disasm would render it as "NOP #$02"
	// (or whatever the NMOS slot is). The disassemble handler must route
	// through cpu.DisasmCPU which uses the CPU's own opcode table.
	s, _, out := newStoppedServer(t, nil)
	// Replace the embedded CPU with a CMOS one. newStoppedServer wired
	// an NMOS CPU; for this test we need the CMOS table picked up at
	// construction time (cpu.NewVariant binds it then).
	cmosRAM := s.ram
	cmosRAM.Load(0x8000, []byte{0x80, 0x02})
	cmosRAM.Write(0xFFFC, 0x00)
	cmosRAM.Write(0xFFFD, 0x80)
	s.cpu = newCMOSCPUForTest(t, cmosRAM)

	req := Request{
		ProtocolMessage: ProtocolMessage{Seq: 1, Type: "request"},
		Command:         "disassemble",
		Arguments:       json.RawMessage(`{"memoryReference":"$8000","instructionCount":1}`),
	}
	s.handleDisassemble(req)
	if !strings.Contains(out.String(), `"instruction":"BRA`) {
		t.Fatalf("CMOS BRA should decode via DisasmCPU, got: %s", out.String())
	}
}
