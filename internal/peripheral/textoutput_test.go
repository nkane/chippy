package peripheral

import "testing"

func TestTextOutputBuffersWrites(t *testing.T) {
	out := NewTextOutput(0xF001)
	for _, b := range []byte("HELLO") {
		out.Write(0xF001, b)
	}
	if got := out.String(); got != "HELLO" {
		t.Fatalf("want %q, got %q", "HELLO", got)
	}
	if out.Len() != 5 {
		t.Fatalf("want len 5, got %d", out.Len())
	}
}

func TestTextOutputTranslatesCR(t *testing.T) {
	out := NewTextOutput(0xF001)
	out.Write(0xF001, 'A')
	out.Write(0xF001, 0x0D)
	out.Write(0xF001, 'B')
	if got := out.String(); got != "A\nB" {
		t.Fatalf("want %q, got %q", "A\nB", got)
	}
}

func TestTextOutputStripsHighBit(t *testing.T) {
	out := NewTextOutput(0xF001)
	out.Write(0xF001, 0x80|'X') // Apple-1 monitor sets bit 7
	if got := out.String(); got != "X" {
		t.Fatalf("want %q, got %q", "X", got)
	}
}

func TestTextOutputIgnoresAddrOutsideRange(t *testing.T) {
	// In production, MMIO never routes off-address writes here. Defensive
	// guard exists so test or harness code can't poison the buffer by
	// calling Write directly with a wrong addr.
	out := NewTextOutput(0xF001)
	out.Write(0xF002, 'Z')
	if out.Len() != 0 {
		t.Fatalf("write to wrong addr should not buffer; got %q", out.String())
	}
}

func TestTextOutputRangeAndRead(t *testing.T) {
	out := NewTextOutput(0xF001)
	lo, hi := out.Range()
	if lo != 0xF001 || hi != 0xF001 {
		t.Fatalf("Range want F001,F001; got %04X,%04X", lo, hi)
	}
	if v := out.Read(0xF001); v != 0 {
		t.Fatalf("Read want 0; got %02X", v)
	}
}

func TestTextOutputReset(t *testing.T) {
	out := NewTextOutput(0xF001)
	out.Write(0xF001, 'A')
	out.Reset()
	if out.Len() != 0 {
		t.Fatalf("Reset should empty buffer")
	}
}

func TestTextOutputBytesIsCopy(t *testing.T) {
	out := NewTextOutput(0xF001)
	out.Write(0xF001, 'A')
	b := out.Bytes()
	b[0] = 'Z'
	if out.String() != "A" {
		t.Fatalf("Bytes() must return a copy; internal buffer mutated")
	}
}
