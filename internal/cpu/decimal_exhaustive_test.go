//go:build decimal

// Exhaustive 6502 decimal-mode test. Bruce Clark's canonical BCD test
// (https://6502.org/tutorials/decimal_mode.html Appendix B) walks
// 256 × 256 × 2 input cases through ADC and SBC in decimal mode,
// including invalid BCD inputs (nibbles A-F). The existing
// bcd_test.go covers spot-checks plus a 100 × 100 × 2 valid-BCD
// sweep; this file fills in the invalid-BCD coverage that catches
// the cases real software accidentally hits.
//
// Reference rules per Appendix B:
//
//   - NMOS: only A and C are well-defined. N/V/Z reflect the parallel
//     binary path (a real 6502 quirk).
//   - CMOS: A, C, N, and Z reflect the decimal path. V remains
//     documented-undefined; we don't compare it.
//
// Build-tagged so default `go test` skips the 4 × 262 144 case loops.
// CI runs it via `go test -tags=decimal ./internal/cpu/...`.
package cpu

import (
	"testing"
)

// referenceADC computes (A, C, N, V, Z) for decimal ADC per Appendix B.
// Binary path drives NMOS N/V/Z; the decimal path drives A and C; CMOS
// overrides N/Z from the decimal A.
func referenceADC(n1, n2, cin uint8, cmos bool) (a uint8, c, n, v, z bool) {
	bin := uint16(n1) + uint16(n2) + uint16(cin)
	binByte := byte(bin)
	flagNbin := binByte&0x80 != 0
	flagZbin := binByte == 0
	flagV := ((^(uint16(n1) ^ uint16(n2)))&(uint16(n1)^bin))&0x80 != 0

	al := uint16(n1&0x0F) + uint16(n2&0x0F) + uint16(cin)
	if al >= 0x0A {
		al = ((al + 0x06) & 0x0F) + 0x10
	}
	ah := uint16(n1&0xF0) + uint16(n2&0xF0) + al
	flagC := false
	if ah >= 0xA0 {
		ah += 0x60
	}
	if ah >= 0x100 {
		flagC = true
	}
	a = byte(ah & 0xFF)

	flagN := flagNbin
	flagZ := flagZbin
	if cmos {
		flagN = a&0x80 != 0
		flagZ = a == 0
	}
	return a, flagC, flagN, flagV, flagZ
}

// referenceSBC mirrors referenceADC for decimal SBC. cin acts as
// ~borrow (1 = no borrow, the 6502 convention).
func referenceSBC(n1, n2, cin uint8, cmos bool) (a uint8, c, n, v, z bool) {
	w := uint16(n2 ^ 0xFF)
	bin := uint16(n1) + w + uint16(cin)
	binByte := byte(bin)
	flagC := bin > 0xFF
	flagV := ((^(uint16(n1) ^ w))&(uint16(n1)^bin))&0x80 != 0
	flagNbin := binByte&0x80 != 0
	flagZbin := binByte == 0

	n1l := int(n1 & 0x0F)
	n2l := int(n2 & 0x0F)
	al := n1l - n2l + int(cin) - 1
	if al < 0 {
		al = ((al - 0x06) & 0x0F) - 0x10
	}
	res := int(n1&0xF0) - int(n2&0xF0) + al
	if res < 0 {
		res -= 0x60
	}
	a = byte(res & 0xFF)

	flagN := flagNbin
	flagZ := flagZbin
	if cmos {
		flagN = a&0x80 != 0
		flagZ = a == 0
	}
	return a, flagC, flagN, flagV, flagZ
}

// runDecimalInstruction primes the CPU with one ADC or SBC at $8000
// and runs a single Step. Caller sets c.A + c.P (with D=1) first.
func runDecimalInstruction(c *CPU, ram *RAM, opcode, operand byte) {
	ram.Write(0x8000, opcode)
	ram.Write(0x8001, operand)
	c.PC = 0x8000
	c.Step()
}

func decimalExhaustive(t *testing.T, variant Variant, opcode byte, isSBC bool) {
	t.Helper()
	ram := NewRAM()
	c := NewVariant(ram, variant)
	cmos := variant == VariantCMOS65C02

	cases := 0
	for n1 := 0; n1 < 256; n1++ {
		for n2 := 0; n2 < 256; n2++ {
			for cin := 0; cin < 2; cin++ {
				c.A = byte(n1)
				p := byte(FlagU | FlagD)
				if cin != 0 {
					p |= FlagC
				}
				c.P = p
				runDecimalInstruction(c, ram, opcode, byte(n2))

				var wantA byte
				var wantC, wantN, wantV, wantZ bool
				if isSBC {
					wantA, wantC, wantN, wantV, wantZ = referenceSBC(byte(n1), byte(n2), byte(cin), cmos)
				} else {
					wantA, wantC, wantN, wantV, wantZ = referenceADC(byte(n1), byte(n2), byte(cin), cmos)
				}
				_ = wantV // not compared per Appendix B

				if c.A != wantA {
					t.Fatalf("variant=%v op=$%02X n1=$%02X n2=$%02X cin=%d: A want $%02X got $%02X",
						variant, opcode, n1, n2, cin, wantA, c.A)
				}
				if (c.P&FlagC != 0) != wantC {
					t.Fatalf("variant=%v op=$%02X n1=$%02X n2=$%02X cin=%d: C want %v got %v",
						variant, opcode, n1, n2, cin, wantC, c.P&FlagC != 0)
				}
				if cmos {
					if (c.P&FlagN != 0) != wantN {
						t.Fatalf("variant=%v op=$%02X n1=$%02X n2=$%02X cin=%d: N want %v got %v",
							variant, opcode, n1, n2, cin, wantN, c.P&FlagN != 0)
					}
					if (c.P&FlagZ != 0) != wantZ {
						t.Fatalf("variant=%v op=$%02X n1=$%02X n2=$%02X cin=%d: Z want %v got %v",
							variant, opcode, n1, n2, cin, wantZ, c.P&FlagZ != 0)
					}
				}
				cases++
			}
		}
	}
	t.Logf("exhausted %d cases", cases)
}

func TestDecimal_NMOS_ADC_Exhaustive(t *testing.T) {
	decimalExhaustive(t, VariantNMOS, 0x69, false) // ADC #imm
}

func TestDecimal_NMOS_SBC_Exhaustive(t *testing.T) {
	decimalExhaustive(t, VariantNMOS, 0xE9, true) // SBC #imm
}

func TestDecimal_CMOS_ADC_Exhaustive(t *testing.T) {
	decimalExhaustive(t, VariantCMOS65C02, 0x69, false)
}

func TestDecimal_CMOS_SBC_Exhaustive(t *testing.T) {
	decimalExhaustive(t, VariantCMOS65C02, 0xE9, true)
}
