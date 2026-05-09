// Package tui — conditional breakpoint expressions.
//
// Tiny recursive-descent parser for expressions like:
//
//	A == $FF
//	X > 10 && Y != 0
//	(A & $80) != 0
//	[$0042] == X
//	C && !Z
//	PC >= $E000
//
// Atoms:
//   Registers: A X Y P SP PC
//   Status flag bits (from P): N V B D I Z C   -> 0 or 1
//   Hex literal: $FF / 0xFF
//   Decimal literal
//   Symbol name: resolved via the symbol table at compile time
//   Memory deref: [<addr-expr>]   (8-bit byte read from bus)
//
// Operators (precedence high-to-low):
//   unary: !  -
//   binary: * /
//   binary: + -
//   binary: << >>
//   binary: &
//   binary: ^
//   binary: |
//   binary: < <= > >=
//   binary: == !=
//   binary: &&
//   binary: ||
//
// The evaluator returns a uint32 internally; the breakpoint check treats
// non-zero as "fire". Result is masked to 32 bits to keep things simple.

package tui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/nkane/chippy/internal/cpu"
	"github.com/nkane/chippy/internal/symbols"
)

type condFn func(*cpu.CPU, cpu.Bus) bool

func compileCondition(src string, syms *symbols.Table) (condFn, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &condParser{toks: toks, syms: syms}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("trailing tokens at %d", p.pos)
	}
	return func(c *cpu.CPU, bus cpu.Bus) bool {
		return expr(c, bus) != 0
	}, nil
}

// ---------- lexer ----------

type tokKind int

const (
	tkNum tokKind = iota
	tkIdent
	tkLParen
	tkRParen
	tkLBracket
	tkRBracket
	tkPlus
	tkMinus
	tkStar
	tkSlash
	tkAmp
	tkPipe
	tkCaret
	tkBang
	tkLT
	tkLE
	tkGT
	tkGE
	tkEQ
	tkNE
	tkAndAnd
	tkOrOr
	tkShl
	tkShr
)

type tok struct {
	kind tokKind
	val  uint32 // for tkNum
	id   string // for tkIdent
}

func tokenize(s string) ([]tok, error) {
	var out []tok
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case unicode.IsSpace(rune(c)):
			i++
		case c == '(':
			out = append(out, tok{kind: tkLParen})
			i++
		case c == ')':
			out = append(out, tok{kind: tkRParen})
			i++
		case c == '[':
			out = append(out, tok{kind: tkLBracket})
			i++
		case c == ']':
			out = append(out, tok{kind: tkRBracket})
			i++
		case c == '+':
			out = append(out, tok{kind: tkPlus})
			i++
		case c == '-':
			out = append(out, tok{kind: tkMinus})
			i++
		case c == '*':
			out = append(out, tok{kind: tkStar})
			i++
		case c == '/':
			out = append(out, tok{kind: tkSlash})
			i++
		case c == '^':
			out = append(out, tok{kind: tkCaret})
			i++
		case c == '!':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, tok{kind: tkNE})
				i += 2
			} else {
				out = append(out, tok{kind: tkBang})
				i++
			}
		case c == '=':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, tok{kind: tkEQ})
				i += 2
			} else {
				return nil, fmt.Errorf("stray '='")
			}
		case c == '<':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, tok{kind: tkLE})
				i += 2
			} else if i+1 < len(s) && s[i+1] == '<' {
				out = append(out, tok{kind: tkShl})
				i += 2
			} else {
				out = append(out, tok{kind: tkLT})
				i++
			}
		case c == '>':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, tok{kind: tkGE})
				i += 2
			} else if i+1 < len(s) && s[i+1] == '>' {
				out = append(out, tok{kind: tkShr})
				i += 2
			} else {
				out = append(out, tok{kind: tkGT})
				i++
			}
		case c == '&':
			if i+1 < len(s) && s[i+1] == '&' {
				out = append(out, tok{kind: tkAndAnd})
				i += 2
			} else {
				out = append(out, tok{kind: tkAmp})
				i++
			}
		case c == '|':
			if i+1 < len(s) && s[i+1] == '|' {
				out = append(out, tok{kind: tkOrOr})
				i += 2
			} else {
				out = append(out, tok{kind: tkPipe})
				i++
			}
		case c == '$':
			j := i + 1
			for j < len(s) && isHex(s[j]) {
				j++
			}
			if j == i+1 {
				return nil, fmt.Errorf("empty hex literal at %d", i)
			}
			v, err := parseHex(s[i+1 : j])
			if err != nil {
				return nil, err
			}
			out = append(out, tok{kind: tkNum, val: v})
			i = j
		case c == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X'):
			j := i + 2
			for j < len(s) && isHex(s[j]) {
				j++
			}
			v, err := parseHex(s[i+2 : j])
			if err != nil {
				return nil, err
			}
			out = append(out, tok{kind: tkNum, val: v})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			var v uint32
			for k := i; k < j; k++ {
				v = v*10 + uint32(s[k]-'0')
			}
			out = append(out, tok{kind: tkNum, val: v})
			i = j
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentCont(s[j]) {
				j++
			}
			out = append(out, tok{kind: tkIdent, id: s[i:j]})
			i = j
		default:
			return nil, fmt.Errorf("unexpected %q at %d", c, i)
		}
	}
	return out, nil
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}
func isIdentCont(c byte) bool { return isIdentStart(c) || (c >= '0' && c <= '9') }

// ---------- parser ----------

type evalFn func(*cpu.CPU, cpu.Bus) uint32

type condParser struct {
	toks []tok
	pos  int
	syms *symbols.Table
}

func (p *condParser) peek() (tok, bool) {
	if p.pos >= len(p.toks) {
		return tok{}, false
	}
	return p.toks[p.pos], true
}
func (p *condParser) accept(k tokKind) bool {
	if t, ok := p.peek(); ok && t.kind == k {
		p.pos++
		return true
	}
	return false
}

// or := and ('||' and)*
func (p *condParser) parseOr() (evalFn, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.accept(tkOrOr) {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			if l(c, bus) != 0 || r(c, bus) != 0 {
				return 1
			}
			return 0
		}
	}
	return left, nil
}

// and := eq ('&&' eq)*
func (p *condParser) parseAnd() (evalFn, error) {
	left, err := p.parseEq()
	if err != nil {
		return nil, err
	}
	for p.accept(tkAndAnd) {
		right, err := p.parseEq()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			if l(c, bus) != 0 && r(c, bus) != 0 {
				return 1
			}
			return 0
		}
	}
	return left, nil
}

// eq := rel (('==' | '!=') rel)*
func (p *condParser) parseEq() (evalFn, error) {
	left, err := p.parseRel()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tkEQ && t.kind != tkNE) {
			break
		}
		p.pos++
		right, err := p.parseRel()
		if err != nil {
			return nil, err
		}
		op := t.kind
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			lv, rv := l(c, bus), r(c, bus)
			if (op == tkEQ && lv == rv) || (op == tkNE && lv != rv) {
				return 1
			}
			return 0
		}
	}
	return left, nil
}

// rel := bitOr (('<' | '<=' | '>' | '>=') bitOr)*
func (p *condParser) parseRel() (evalFn, error) {
	left, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tkLT && t.kind != tkLE && t.kind != tkGT && t.kind != tkGE) {
			break
		}
		p.pos++
		right, err := p.parseBitOr()
		if err != nil {
			return nil, err
		}
		op := t.kind
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			lv, rv := l(c, bus), r(c, bus)
			var b bool
			switch op {
			case tkLT:
				b = lv < rv
			case tkLE:
				b = lv <= rv
			case tkGT:
				b = lv > rv
			case tkGE:
				b = lv >= rv
			}
			if b {
				return 1
			}
			return 0
		}
	}
	return left, nil
}

// bitOr := bitXor ('|' bitXor)*
func (p *condParser) parseBitOr() (evalFn, error) {
	left, err := p.parseBitXor()
	if err != nil {
		return nil, err
	}
	for p.accept(tkPipe) {
		right, err := p.parseBitXor()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 { return l(c, bus) | r(c, bus) }
	}
	return left, nil
}

// bitXor := bitAnd ('^' bitAnd)*
func (p *condParser) parseBitXor() (evalFn, error) {
	left, err := p.parseBitAnd()
	if err != nil {
		return nil, err
	}
	for p.accept(tkCaret) {
		right, err := p.parseBitAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 { return l(c, bus) ^ r(c, bus) }
	}
	return left, nil
}

// bitAnd := shift ('&' shift)*
func (p *condParser) parseBitAnd() (evalFn, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	for p.accept(tkAmp) {
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 { return l(c, bus) & r(c, bus) }
	}
	return left, nil
}

// shift := add (('<<' | '>>') add)*
func (p *condParser) parseShift() (evalFn, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tkShl && t.kind != tkShr) {
			break
		}
		p.pos++
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		op := t.kind
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			if op == tkShl {
				return l(c, bus) << (r(c, bus) & 31)
			}
			return l(c, bus) >> (r(c, bus) & 31)
		}
	}
	return left, nil
}

// add := mul (('+' | '-') mul)*
func (p *condParser) parseAdd() (evalFn, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tkPlus && t.kind != tkMinus) {
			break
		}
		p.pos++
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		op := t.kind
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			if op == tkPlus {
				return l(c, bus) + r(c, bus)
			}
			return l(c, bus) - r(c, bus)
		}
	}
	return left, nil
}

// mul := unary (('*' | '/') unary)*
func (p *condParser) parseMul() (evalFn, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		t, ok := p.peek()
		if !ok || (t.kind != tkStar && t.kind != tkSlash) {
			break
		}
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		op := t.kind
		l, r := left, right
		left = func(c *cpu.CPU, bus cpu.Bus) uint32 {
			rv := r(c, bus)
			if op == tkStar {
				return l(c, bus) * rv
			}
			if rv == 0 {
				return 0
			}
			return l(c, bus) / rv
		}
	}
	return left, nil
}

// unary := ('!' | '-') unary | primary
func (p *condParser) parseUnary() (evalFn, error) {
	if p.accept(tkBang) {
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(c *cpu.CPU, bus cpu.Bus) uint32 {
			if x(c, bus) == 0 {
				return 1
			}
			return 0
		}, nil
	}
	if p.accept(tkMinus) {
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(c *cpu.CPU, bus cpu.Bus) uint32 { return uint32(-int32(x(c, bus))) }, nil
	}
	return p.parsePrimary()
}

// primary := num | '(' or ')' | '[' or ']' | ident
func (p *condParser) parsePrimary() (evalFn, error) {
	t, ok := p.peek()
	if !ok {
		return nil, fmt.Errorf("unexpected end of expression")
	}
	switch t.kind {
	case tkNum:
		p.pos++
		v := t.val
		return func(*cpu.CPU, cpu.Bus) uint32 { return v }, nil
	case tkLParen:
		p.pos++
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.accept(tkRParen) {
			return nil, fmt.Errorf("missing ')'")
		}
		return e, nil
	case tkLBracket:
		p.pos++
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.accept(tkRBracket) {
			return nil, fmt.Errorf("missing ']'")
		}
		return func(c *cpu.CPU, bus cpu.Bus) uint32 {
			return uint32(bus.Read(uint16(e(c, bus) & 0xFFFF)))
		}, nil
	case tkIdent:
		p.pos++
		return p.identEval(t.id)
	}
	return nil, fmt.Errorf("unexpected token at %d", p.pos)
}

func (p *condParser) identEval(name string) (evalFn, error) {
	switch strings.ToUpper(name) {
	case "A":
		return func(c *cpu.CPU, _ cpu.Bus) uint32 { return uint32(c.A) }, nil
	case "X":
		return func(c *cpu.CPU, _ cpu.Bus) uint32 { return uint32(c.X) }, nil
	case "Y":
		return func(c *cpu.CPU, _ cpu.Bus) uint32 { return uint32(c.Y) }, nil
	case "P":
		return func(c *cpu.CPU, _ cpu.Bus) uint32 { return uint32(c.P) }, nil
	case "SP", "S":
		return func(c *cpu.CPU, _ cpu.Bus) uint32 { return uint32(c.SP) }, nil
	case "PC":
		return func(c *cpu.CPU, _ cpu.Bus) uint32 { return uint32(c.PC) }, nil
	// Status flag bits, masked out of P.
	case "N":
		return flagBit(0x80), nil
	case "V":
		return flagBit(0x40), nil
	case "B":
		return flagBit(0x10), nil
	case "D":
		return flagBit(0x08), nil
	case "I":
		return flagBit(0x04), nil
	case "Z":
		return flagBit(0x02), nil
	case "C":
		return flagBit(0x01), nil
	}
	if p.syms != nil {
		if addr, ok := p.syms.LookupName(name); ok {
			a := uint32(addr)
			return func(*cpu.CPU, cpu.Bus) uint32 { return a }, nil
		}
	}
	return nil, fmt.Errorf("unknown name: %s", name)
}

func flagBit(mask byte) evalFn {
	return func(c *cpu.CPU, _ cpu.Bus) uint32 {
		if c.P&mask != 0 {
			return 1
		}
		return 0
	}
}
