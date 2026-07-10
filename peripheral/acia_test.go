package peripheral

import "testing"

// fakeIRQ records AssertIRQSource/ClearIRQSource calls for the named source.
type fakeIRQ struct{ asserted bool }

func (f *fakeIRQ) AssertIRQSource(string) { f.asserted = true }
func (f *fakeIRQ) ClearIRQSource(string)  { f.asserted = false }

func TestACIA_TransmitToSink(t *testing.T) {
	a := NewACIA(0x5000)
	for _, b := range []byte("Hi") {
		a.Write(0x5000, b) // DATA
	}
	if got := a.TxString(); got != "Hi" {
		t.Fatalf("tx = %q want %q", got, "Hi")
	}
	// TDRE (transmit ready) is always set — polled TX never blocks.
	if a.Read(0x5001)&aciaStatusTDRE == 0 {
		t.Fatal("TDRE should always be set")
	}
}

func TestACIA_ReceiveDequeues(t *testing.T) {
	a := NewACIA(0x5000)
	a.Receive('A')
	a.Receive('B')
	if a.Read(0x5001)&aciaStatusRDRF == 0 {
		t.Fatal("RDRF should be set with bytes queued")
	}
	if got := a.Read(0x5000); got != 'A' {
		t.Fatalf("first DATA read = %q want 'A'", got)
	}
	if got := a.Read(0x5000); got != 'B' {
		t.Fatalf("second DATA read = %q want 'B'", got)
	}
	// Queue drained -> RDRF clear.
	if a.Read(0x5001)&aciaStatusRDRF != 0 {
		t.Fatal("RDRF should clear once the queue drains")
	}
}

func TestACIA_ReceiveIRQ(t *testing.T) {
	a := NewACIA(0x5000)
	irq := &fakeIRQ{}
	a.SetIRQ(irq)
	// COMMAND bit 1 = 0 enables receiver IRQ.
	a.Write(0x5002, 0x00)

	a.Receive('X')
	if !irq.asserted {
		t.Fatal("receiving a byte with RX IRQ enabled should assert the line")
	}
	if a.Read(0x5001)&aciaStatusIRQ == 0 {
		t.Fatal("STATUS bit 7 should flag the IRQ")
	}
	// Reading STATUS clears the IRQ.
	if irq.asserted {
		t.Fatal("reading STATUS should release the IRQ line")
	}
}

func TestACIA_ReceiveIRQ_Disabled(t *testing.T) {
	a := NewACIA(0x5000)
	irq := &fakeIRQ{}
	a.SetIRQ(irq)
	a.Write(0x5002, 0x02) // COMMAND bit 1 set -> RX IRQ disabled

	a.Receive('X')
	if irq.asserted {
		t.Fatal("RX IRQ disabled: no assertion")
	}
}

func TestACIA_ReadDataClearsIRQ(t *testing.T) {
	a := NewACIA(0x5000)
	irq := &fakeIRQ{}
	a.SetIRQ(irq)
	a.Write(0x5002, 0x00)
	a.Receive('Z')
	if !irq.asserted {
		t.Fatal("precondition: IRQ asserted")
	}
	if got := a.Read(0x5000); got != 'Z' { // reading the last byte clears IRQ
		t.Fatalf("DATA = %q want 'Z'", got)
	}
	if irq.asserted {
		t.Fatal("reading the final RX byte should release the IRQ line")
	}
}

func TestACIA_Peek_NoSideEffects(t *testing.T) {
	a := NewACIA(0x5000)
	a.Receive('Q')
	if got := a.Peek(0x5000); got != 'Q' {
		t.Fatalf("peek DATA = %q want 'Q'", got)
	}
	// Peek must not dequeue.
	if a.RxPending() != 1 {
		t.Fatalf("peek dequeued: pending=%d want 1", a.RxPending())
	}
	if a.Peek(0x5001)&aciaStatusRDRF == 0 {
		t.Fatal("peek STATUS should still show RDRF")
	}
}

func TestACIA_SnapshotRoundTrip(t *testing.T) {
	a := NewACIA(0x5000)
	a.Write(0x5002, 0x09) // command
	a.Write(0x5003, 0x1E) // control
	a.Write(0x5000, 'a')  // tx
	a.Write(0x5000, 'b')
	a.Receive('r')
	snap := a.Snapshot()

	b := NewACIA(0x5000)
	b.Restore(snap)
	if b.command != 0x09 || b.control != 0x1E {
		t.Fatalf("restore regs: cmd=$%02X ctrl=$%02X", b.command, b.control)
	}
	if b.TxString() != "ab" {
		t.Fatalf("restore tx = %q want %q", b.TxString(), "ab")
	}
	if b.RxPending() != 1 || b.Read(0x5000) != 'r' {
		t.Fatal("restore rx queue mismatch")
	}
}

// The ACIA satisfies the peripheral contract (structural check).
func TestACIA_ImplementsInterfaces(t *testing.T) {
	var _ interface {
		Range() (uint16, uint16)
		Read(uint16) byte
		Write(uint16, byte)
	} = NewACIA(0)
	var _ Snapshotable = NewACIA(0)
}
