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

// Cap=0 keeps the unbounded legacy behavior for tests that want it.
func TestTextOutputUnboundedWhenCapZero(t *testing.T) {
	out := NewTextOutputWithCap(0xF001, 0)
	for i := 0; i < 1<<17; i++ { // 128 KiB — exceeds default cap
		out.Write(0xF001, byte('A'+i%26))
	}
	if out.Len() != 1<<17 {
		t.Fatalf("Cap=0 should not bound; len=%d", out.Len())
	}
}

// Cap bounds the buffer; writes past Cap evict oldest bytes. Confirms
// total length stays under Cap and the most-recent writes are retained.
func TestTextOutputCapBoundsLength(t *testing.T) {
	const cap = 16
	out := NewTextOutputWithCap(0xF001, cap)
	for i := 0; i < 1000; i++ {
		out.Write(0xF001, byte('A'+i%26))
	}
	if out.Len() > cap {
		t.Fatalf("len exceeded cap: %d > %d", out.Len(), cap)
	}
	if out.Len() < cap/2 {
		t.Fatalf("len below half-cap after many writes: %d", out.Len())
	}
	// Final byte must be the last one we wrote (i=999, 999%26 = 11, 'A'+11 = 'L').
	if last := out.Bytes()[out.Len()-1]; last != 'L' {
		t.Fatalf("tail byte want 'L'; got %q", last)
	}
}

// Default constructor picks the 64 KiB cap.
func TestTextOutputDefaultCap(t *testing.T) {
	out := NewTextOutput(0xF001)
	if out.Cap != DefaultTextOutputCap {
		t.Fatalf("default Cap want %d; got %d", DefaultTextOutputCap, out.Cap)
	}
}
