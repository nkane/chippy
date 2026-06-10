// Package expr — tiny recursive-descent parser for breakpoint conditions
// and DAP `evaluate` requests. Shared between internal/tui (where it
// gates `:bp X if E` and `:bpr X if E`) and internal/dap (where it
// powers the watch panel, hover-evaluate, and the debug console).
//
// Atoms:
//
//	Registers: A X Y P SP PC
//	Status flag bits (from P): N V B D I Z C   -> 0 or 1
//	Hex literal: $FF / 0xFF
//	Decimal literal
//	Symbol name: resolved via the symbol table at compile time
//	Memory deref: [<addr-expr>]   (8-bit byte read from bus)
//
// Operators (precedence high-to-low):
//
//	unary: !  -
//	binary: * /
//	binary: + -
//	binary: << >>
//	binary: &
//	binary: ^
//	binary: |
//	binary: < <= > >=
//	binary: == !=
//	binary: &&
//	binary: ||
//
// The evaluator returns a uint32 internally; callers that need a bool
// (breakpoint condition) treat non-zero as "fire". Result is masked to
// 32 bits to keep things simple.
//
// Unary minus is width-aware so register-byte comparisons read naturally:
// `-1` evaluates to `$FF` (not `$FFFFFFFF`), `-$0100` evaluates to
// `$FF00`, etc. Binary subtraction stays 32-bit modular — wrap `(- v &
// $FFFF)` explicitly if a different width is needed.
package expr

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/nkane/chippy/cpu"
	"github.com/nkane/chippy/symbols"
)

// EvalFn computes a 32-bit value from current CPU + bus state. Used as a
// numeric evaluator (DAP evaluate) or wrapped into a bool predicate
// (breakpoint conditions).
type EvalFn func(*cpu.CPU, cpu.Bus) uint32

// HostVarResolver lets a hosting process expose named runtime values to the
// expression evaluator (issue #433). Given an identifier it returns a getter
// that reads the live value, or false when it doesn't know the name. The
// getter is invoked at eval time, so it sees current host state — e.g. nessy
// resolves `scanline` / `dot` / `frame` against its PPU so a conditional
// breakpoint like `scanline == 30` works against state the 6502 core can't see.
type HostVarResolver func(name string) (get func() uint32, ok bool)

// Compile parses src into an EvalFn. Symbol references resolve against syms at
// compile time so the evaluator stays allocation-free at hot-path breakpoint
// check time. An optional HostVarResolver supplies host-defined identifiers
// (resolved after CPU registers/flags, before symbols).
func Compile(src string, syms *symbols.Table, host ...HostVarResolver) (EvalFn, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &condParser{toks: toks, syms: syms}
	if len(host) > 0 {
		p.host = host[0]
	}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.toks) {
		return nil, fmt.Errorf("trailing tokens at %d", p.pos)
	}
	return expr, nil
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

func parseHex(s string) (uint32, error) {
	var v uint32
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint32(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint32(c-'A') + 10
		default:
			return 0, fmt.Errorf("bad hex digit %q", c)
		}
	}
	return v, nil
}

// ---------- parser ----------

type condParser struct {
	toks []tok
	pos  int
	syms *symbols.Table
	host HostVarResolver
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

func (p *condParser) parseOr() (EvalFn, error) {
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

func (p *condParser) parseAnd() (EvalFn, error) {
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

func (p *condParser) parseEq() (EvalFn, error) {
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

func (p *condParser) parseRel() (EvalFn, error) {
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

func (p *condParser) parseBitOr() (EvalFn, error) {
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

func (p *condParser) parseBitXor() (EvalFn, error) {
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

func (p *condParser) parseBitAnd() (EvalFn, error) {
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

func (p *condParser) parseShift() (EvalFn, error) {
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

func (p *condParser) parseAdd() (EvalFn, error) {
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

func (p *condParser) parseMul() (EvalFn, error) {
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

func (p *condParser) parseUnary() (EvalFn, error) {
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
		// Width-aware negation. Picking the smallest power-of-two width
		// that contains the operand makes `-1` round to `$FF` (byte
		// negation), so `A == -1` matches a register holding $FF rather
		// than the 32-bit `$FFFFFFFF` that a naive `-int32(v)` would
		// emit. Larger operands keep their natural width: `-$0100`
		// produces `$FF00`, `-$10000` produces `$FFFF0000`.
		return func(c *cpu.CPU, bus cpu.Bus) uint32 {
			v := x(c, bus)
			switch {
			case v == 0:
				return 0
			case v <= 0xFF:
				return uint32(byte(0 - v))
			case v <= 0xFFFF:
				return uint32(uint16(0 - v))
			default:
				return 0 - v
			}
		}, nil
	}
	return p.parsePrimary()
}

func (p *condParser) parsePrimary() (EvalFn, error) {
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

func (p *condParser) identEval(name string) (EvalFn, error) {
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
	if p.host != nil {
		if get, ok := p.host(name); ok {
			return func(*cpu.CPU, cpu.Bus) uint32 { return get() }, nil
		}
	}
	if p.syms != nil {
		if addr, ok := p.syms.LookupName(name); ok {
			a := uint32(addr)
			return func(*cpu.CPU, cpu.Bus) uint32 { return a }, nil
		}
	}
	return nil, fmt.Errorf("unknown name: %s", name)
}

func flagBit(mask byte) EvalFn {
	return func(c *cpu.CPU, _ cpu.Bus) uint32 {
		if c.P&mask != 0 {
			return 1
		}
		return 0
	}
}
