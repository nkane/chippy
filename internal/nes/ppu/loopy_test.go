package ppu

import "testing"

// incCoarseX bumps v's coarse-X bits. At coarse-X = 31, it wraps
// to 0 + flips the horizontal nametable bit (10).
func TestLoopy_IncCoarseXWrapsAndFlipsNT(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// Mid-range coarse-X.
	p.v = 0x0010
	p.incCoarseX()
	if p.v != 0x0011 {
		t.Errorf("post-bump coarse-X = $%04X; want $0011", p.v)
	}
	// At wrap point.
	p.v = 0x001F
	p.incCoarseX()
	if p.v&0x001F != 0 {
		t.Errorf("coarse-X didn't wrap to 0; got %d", p.v&0x001F)
	}
	if p.v&0x0400 == 0 {
		t.Errorf("horizontal nametable bit not flipped on wrap")
	}
}

// incY advances fine-Y, then coarse-Y at fine-Y rollover. At
// coarse-Y = 29 it wraps + flips vertical nametable bit; at 31
// (attribute table) it wraps without flipping.
func TestLoopy_IncYAdvancesAndFlipsNT(t *testing.T) {
	p := New(&fakeCart{}, nil)
	// fine-Y mid-range.
	p.v = 0x1000 // fine-Y = 1
	p.incY()
	if p.v != 0x2000 {
		t.Errorf("fine-Y bump = $%04X; want $2000", p.v)
	}

	// At fine-Y=7 with coarse-Y=29: roll over to fine-Y=0 +
	// coarse-Y=0 + flip vertical NT bit.
	p.v = 0x73A0 // fine-Y=7, coarse-Y=29
	p.incY()
	if p.v&0x7000 != 0 {
		t.Errorf("fine-Y didn't wrap to 0: $%04X", p.v)
	}
	if (p.v>>5)&0x1F != 0 {
		t.Errorf("coarse-Y didn't wrap to 0 at 29: $%04X", p.v)
	}
	if p.v&0x0800 == 0 {
		t.Errorf("vertical nametable bit not flipped at 29")
	}

	// At fine-Y=7 with coarse-Y=31 (attribute table): wrap
	// coarse-Y to 0 with NO NT bit flip.
	p.v = 0x73E0 // fine-Y=7, coarse-Y=31
	p.incY()
	if (p.v>>5)&0x1F != 0 {
		t.Errorf("coarse-Y didn't wrap from 31")
	}
	// Vertical NT bit shouldn't change here.
	if p.v&0x0800 != 0 {
		t.Errorf("vertical NT bit flipped at 31 (should stay)")
	}
}

// copyXFromT copies coarse-X + horizontal NT bit from t into v
// without disturbing the vertical bits.
func TestLoopy_CopyXFromT(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.t = 0x041F // coarse-X = 31, horizontal NT = 1
	p.v = 0x7800 // vertical bits set
	p.copyXFromT()
	if p.v&0x041F != 0x041F {
		t.Errorf("X bits not copied: v=$%04X", p.v)
	}
	if p.v&0x7800 != 0x7800 {
		t.Errorf("vertical bits clobbered: v=$%04X", p.v)
	}
}

// copyYFromT copies fine-Y + coarse-Y + vertical NT bit from t
// without disturbing horizontal bits.
func TestLoopy_CopyYFromT(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.t = 0x7BE0 // fine-Y=7, coarse-Y=31, vertical NT=1
	p.v = 0x041F // horizontal bits set
	p.copyYFromT()
	if p.v&0x7BE0 != 0x7BE0 {
		t.Errorf("Y bits not copied: v=$%04X", p.v)
	}
	if p.v&0x041F != 0x041F {
		t.Errorf("horizontal bits clobbered: v=$%04X", p.v)
	}
}

// $2000 write should update t bits 10-11 (nametable select) from
// data bits 0-1.
func TestLoopy_2000WriteUpdatesT(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.t = 0x0000
	p.Write(0x2000, 0x03) // bits 0-1 = 11
	if p.t&0x0C00 != 0x0C00 {
		t.Errorf("$2000 write didn't update t NT bits: t=$%04X", p.t)
	}
}

// $2005 two-write sequence sets t coarseX/coarseY/fineY/fineX
// (x latch).
func TestLoopy_2005TwoWriteSequence(t *testing.T) {
	p := New(&fakeCart{}, nil)
	p.Write(0x2002, 0) // reset w (via $2002 read normally; here ensure clean)
	_ = p.Read(0x2002)
	// First write: scrollX = $5D → coarse-X = 11, fine-X = 5.
	p.Write(0x2005, 0x5D)
	if p.t&0x001F != 11 {
		t.Errorf("t coarse-X = %d; want 11", p.t&0x001F)
	}
	if p.x != 5 {
		t.Errorf("x fine-X = %d; want 5", p.x)
	}
	// Second write: scrollY = $4B → coarse-Y = 9, fine-Y = 3.
	p.Write(0x2005, 0x4B)
	if (p.t>>5)&0x1F != 9 {
		t.Errorf("t coarse-Y = %d; want 9", (p.t>>5)&0x1F)
	}
	if (p.t>>12)&0x7 != 3 {
		t.Errorf("t fine-Y = %d; want 3", (p.t>>12)&0x7)
	}
}

// $2006 two-write sequence sets t high then t low + copies t → v.
func TestLoopy_2006TwoWriteSequence(t *testing.T) {
	p := New(&fakeCart{}, nil)
	_ = p.Read(0x2002)
	// First write: high byte $3F → t high = $3F, bit 14 cleared.
	p.Write(0x2006, 0x3F)
	if p.t&0x4000 != 0 {
		t.Errorf("$2006 first write didn't clear bit 14: t=$%04X", p.t)
	}
	// Second write: low byte $C0 → t low = $C0; v ← t.
	p.Write(0x2006, 0xC0)
	if p.t != 0x3FC0 {
		t.Errorf("t after $2006 second write = $%04X; want $3FC0", p.t)
	}
	if p.v != p.t {
		t.Errorf("v not copied from t: v=$%04X t=$%04X", p.v, p.t)
	}
}
