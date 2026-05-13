package trace

import (
	"strings"
	"testing"
)

const sampleTrace = `8000  A9 42         LDA #$42         A:00 X:00 Y:00 P:24 SP:FD CYC:7
8002  AA            TAX              A:42 X:42 Y:00 P:24 SP:FD CYC:9
---- IRQ -> $FFFE (PC=$8003 P=24 SP=FD CYC:11)
9000  48            PHA              A:42 X:42 Y:00 P:24 SP:FB CYC:18
9001  40            RTI              A:42 X:42 Y:00 P:24 SP:FB CYC:24
`

func TestParse_BasicFrames(t *testing.T) {
	r, err := Parse(strings.NewReader(sampleTrace))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Len() != 4 {
		t.Fatalf("want 4 frames; got %d", r.Len())
	}
	if r.Frames[0].PC != 0x8000 || r.Frames[0].Mnemonic != "LDA #$42" {
		t.Errorf("frame[0] wrong: %+v", r.Frames[0])
	}
	if r.Frames[1].A != 0x42 || r.Frames[1].X != 0x42 {
		t.Errorf("frame[1] regs wrong: %+v", r.Frames[1])
	}
	// Interrupt marker rolls onto the NEXT frame.
	if r.Frames[2].InterruptIn != "IRQ" {
		t.Errorf("frame[2] interrupt tag missing: %+v", r.Frames[2])
	}
	if r.Frames[3].InterruptIn != "" {
		t.Errorf("frame[3] interrupt should be empty after first carry: %+v", r.Frames[3])
	}
}

func TestReplay_StepAndSeek(t *testing.T) {
	r, _ := Parse(strings.NewReader(sampleTrace))
	if got, _ := r.Current(); got.PC != 0x8000 {
		t.Fatalf("starting frame PC want $8000; got $%04X", got.PC)
	}
	if !r.Step(2) {
		t.Fatalf("Step(2) should move")
	}
	if got, _ := r.Current(); got.PC != 0x9000 {
		t.Fatalf("after Step(2) PC want $9000; got $%04X", got.PC)
	}
	// Past the end → clamp.
	r.Step(100)
	if got, _ := r.Current(); got.PC != 0x9001 {
		t.Fatalf("clamped end PC want $9001; got $%04X", got.PC)
	}
	// Negative clamp.
	r.Step(-100)
	if got, _ := r.Current(); got.PC != 0x8000 {
		t.Fatalf("clamped start PC want $8000; got $%04X", got.PC)
	}
	// Seek by address.
	if !r.Seek(0x9000) {
		t.Fatalf("Seek($9000) should find a frame")
	}
	if got, _ := r.Current(); got.PC != 0x9000 {
		t.Fatalf("after Seek PC want $9000; got $%04X", got.PC)
	}
	if r.Seek(0xDEAD) {
		t.Fatalf("Seek($DEAD) should miss")
	}
}

func TestParse_EmptyAndMalformedLines(t *testing.T) {
	in := "\n\n8000  EA            NOP              A:00 X:00 Y:00 P:24 SP:FD CYC:5\n"
	r, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("want 1 frame; got %d", r.Len())
	}
}
