package core

import (
	"fmt"
	"strconv"
	"strings"
)

// Py — the ".py frontend": a small, honest Python subset that compiles to the
// SAME []BCInstr the game bytecode VM (bytecode.go) already executes.
//
// It is deliberately tiny. The point is that a player's .py file becomes real,
// capability-checked game bytecode — file reads/writes and sockets still pass
// the same VFS permission gates and the same Dial gates as every other
// program — not that Python is emulated. No host code is ever executed.
//
// Supported (this is the whole language):
//
//	x = <int expr>          integer assignment
//	print(<int expr>)       print an integer
//	print("literal")        print a string literal
//	if / elif / else:       bodies on their own indented lines
//	while:                  integer loop
//	+ - * / %               arithmetic; / is integer division (like //),
//	                        % truncates toward zero for negative operands
//	== != < <= > >=         one comparison per condition (no and/or/not)
//	integer literals, ( ), unary minus
//
// Stated limits (model limits, not bugs):
//   - no floats, no strings outside print, no input()/import/def/class/for/
//     comprehensions/lists/booleans. Each unsupported construct is refused by
//     name with a py:<line>: error rather than crashing.
//   - variables live in the integer registers R1..R7, so a program has at
//     most seven. R0 is reserved (OPEN_SOCKET and SPAWN write it). The ISA's
//     memory cells (Mem[]) are write-only — STORE/STORER set them, and there
//     is no load-from-memory op — so a variable cannot honestly live there.
//   - division/modulo by zero is the VM's own runtime fault (exit 1).
//
// Errors are formatted like the assembler: py:<line>: <reason>.

type pyErr struct {
	line int
	msg  string
}

func (e *pyErr) Error() string { return fmt.Sprintf("py:%d: %s", e.line, e.msg) }

func pyFail(line int, format string, a ...any) {
	panic(&pyErr{line: line, msg: fmt.Sprintf(format, a...)})
}

// CompilePy compiles the subset above to instructions the VM runs. The
// returned code is plain data, so it can be inspected, saved, or handed to
// BCRun exactly like BCAssemble's output.
func CompilePy(src string) (code []BCInstr, err error) {
	defer func() {
		if r := recover(); r != nil {
			if pe, ok := r.(*pyErr); ok {
				code, err = nil, pe
				return
			}
			panic(r)
		}
	}()
	c := &pyCompiler{
		toks:     lexPy(src),
		vars:     map[string]int{},
		varReg:   map[int]bool{},
		tempUsed: map[int]bool{},
	}
	c.parseProgram()
	c.emit(BCInstr{Op: "HALT", A: 0, J: -1})
	return c.ins, nil
}

// ---- lexer -----------------------------------------------------------------

// ptok is one token; kinds are int, str, ident, op, nl, indent, dedent, eof.
// Keywords stay idents — the parser names them.
type ptok struct {
	kind string
	val  string
	line int
}

func isPyDigit(b byte) bool { return b >= '0' && b <= '9' }
func isPyIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
func isPyIdentPart(b byte) bool { return isPyIdentStart(b) || isPyDigit(b) }

// stripPyComment removes a # comment, leaving # inside string literals alone.
func stripPyComment(s string) string {
	var inS byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inS != 0 {
			if c == '\\' {
				i++
				continue
			}
			if c == inS {
				inS = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			inS = c
		case '#':
			return s[:i]
		}
	}
	return s
}

// lexPy turns source into a token stream with NEWLINE/INDENT/DEDENT tokens,
// the way Python's own tokenizer does. Blank and comment-only lines produce
// nothing; tabs in indentation are refused by name.
func lexPy(src string) []ptok {
	lines := strings.Split(src, "\n")
	var toks []ptok
	indents := []int{0}
	for i, raw := range lines {
		ln := i + 1
		j := 0
		for j < len(raw) && raw[j] == ' ' {
			j++
		}
		if j < len(raw) && raw[j] == '\t' {
			pyFail(ln, "tab indentation is not supported")
		}
		indent := j
		body := stripPyComment(raw[j:])
		body = strings.TrimRight(body, " \t\r")
		if body == "" {
			continue
		}
		switch {
		case indent > indents[len(indents)-1]:
			indents = append(indents, indent)
			toks = append(toks, ptok{kind: "indent", line: ln})
		case indent < indents[len(indents)-1]:
			for len(indents) > 1 && indent < indents[len(indents)-1] {
				indents = indents[:len(indents)-1]
				toks = append(toks, ptok{kind: "dedent", line: ln})
			}
			if indent != indents[len(indents)-1] {
				pyFail(ln, "unindent does not match any outer indentation level")
			}
		}
		toks = append(toks, lexPyLine(body, ln)...)
		toks = append(toks, ptok{kind: "nl", line: ln})
	}
	if len(toks) == 0 || toks[len(toks)-1].kind != "nl" {
		toks = append(toks, ptok{kind: "nl", line: len(lines)})
	}
	for len(indents) > 1 {
		indents = indents[:len(indents)-1]
		toks = append(toks, ptok{kind: "dedent", line: len(lines)})
	}
	toks = append(toks, ptok{kind: "eof", line: len(lines)})
	return toks
}

// lexPyLine tokenizes the body of one non-blank line.
func lexPyLine(body string, ln int) []ptok {
	var toks []ptok
	for i := 0; i < len(body); {
		ch := body[i]
		if ch == ' ' || ch == '\t' {
			i++
			continue
		}
		if isPyDigit(ch) {
			j := i
			for j < len(body) && isPyDigit(body[j]) {
				j++
			}
			if j < len(body) && (body[j] == '.' || body[j] == '_' || body[j] == 'e' || body[j] == 'E') {
				pyFail(ln, "floats are not supported (integer subset)")
			}
			if j < len(body) && isPyIdentPart(body[j]) {
				pyFail(ln, "bad number %q", body[i:j+1])
			}
			toks = append(toks, ptok{kind: "int", val: body[i:j], line: ln})
			i = j
			continue
		}
		if isPyIdentStart(ch) {
			j := i
			for j < len(body) && isPyIdentPart(body[j]) {
				j++
			}
			toks = append(toks, ptok{kind: "ident", val: body[i:j], line: ln})
			i = j
			continue
		}
		if ch == '"' || ch == '\'' {
			quote := ch
			j := i + 1
			var sb strings.Builder
			closed := false
			for j < len(body) {
				c := body[j]
				if c == '\\' {
					if j+1 >= len(body) {
						break
					}
					switch e := body[j+1]; e {
					case 'n':
						sb.WriteByte('\n')
					case 't':
						sb.WriteByte('\t')
					case 'r':
						sb.WriteByte('\r')
					case '\\':
						sb.WriteByte('\\')
					case '"':
						sb.WriteByte('"')
					case '\'':
						sb.WriteByte('\'')
					default:
						pyFail(ln, "unknown escape \\%c in string", e)
					}
					j += 2
					continue
				}
				if c == quote {
					closed = true
					j++
					break
				}
				sb.WriteByte(c)
				j++
			}
			if !closed {
				pyFail(ln, "unterminated string literal")
			}
			toks = append(toks, ptok{kind: "str", val: sb.String(), line: ln})
			i = j
			continue
		}
		if i+1 < len(body) {
			switch body[i : i+2] {
			case "==", "!=", "<=", ">=":
				toks = append(toks, ptok{kind: "op", val: body[i : i+2], line: ln})
				i += 2
				continue
			}
		}
		switch ch {
		case '=', '<', '>', '+', '-', '*', '/', '%', '(', ')', ':':
			toks = append(toks, ptok{kind: "op", val: string(ch), line: ln})
			i++
			continue
		}
		pyFail(ln, "unexpected character %q", string(ch))
	}
	return toks
}

// ---- compiler --------------------------------------------------------------

type pyCompiler struct {
	toks []ptok
	pos  int
	line int

	ins []BCInstr
	// vars maps a name to its (fixed) register; varReg marks those registers
	// so a temporary is never allocated on top of a variable.
	vars     map[string]int
	varReg   map[int]bool
	tempUsed map[int]bool
}

func (c *pyCompiler) emit(in BCInstr) int {
	c.ins = append(c.ins, in)
	return len(c.ins) - 1
}

func (c *pyCompiler) here() int { return len(c.ins) }

func (c *pyCompiler) patch(idx, target int) {
	if idx < 0 || idx >= len(c.ins) {
		return
	}
	c.ins[idx].J = target
}

// allocTemp hands out the lowest register that is neither a variable nor
// already a live temporary. R0 is skipped: the VM writes it on OPEN_SOCKET and
// SPAWN, so it is not ours to spend.
func (c *pyCompiler) allocTemp(line int) int {
	for r := 1; r <= 7; r++ {
		if !c.varReg[r] && !c.tempUsed[r] {
			c.tempUsed[r] = true
			return r
		}
	}
	pyFail(line, "expression too complex (out of registers)")
	return 0
}

func (c *pyCompiler) freeTemp(r int) { delete(c.tempUsed, r) }

// defineVar returns a variable's register, allocating it on first assignment.
func (c *pyCompiler) defineVar(name string, line int) int {
	if r, ok := c.vars[name]; ok {
		return r
	}
	for r := 1; r <= 7; r++ {
		if !c.varReg[r] && !c.tempUsed[r] {
			c.varReg[r] = true
			c.vars[name] = r
			return r
		}
	}
	pyFail(line, "too many variables (max 7)")
	return 0
}

func (c *pyCompiler) peek() ptok { return c.toks[c.pos] }

func (c *pyCompiler) next() ptok {
	t := c.toks[c.pos]
	if c.pos < len(c.toks)-1 {
		c.pos++
	}
	c.line = t.line
	return t
}

func (c *pyCompiler) expectOp(val string) {
	t := c.next()
	if t.kind != "op" || t.val != val {
		pyFail(t.line, "expected %q", val)
	}
}

func (c *pyCompiler) expectNL() {
	t := c.next()
	if t.kind != "nl" {
		pyFail(t.line, "expected end of line")
	}
}

// ---- statements ------------------------------------------------------------

func (c *pyCompiler) parseProgram() {
	for c.peek().kind != "eof" {
		switch c.peek().kind {
		case "nl":
			c.next()
		case "indent":
			pyFail(c.peek().line, "unexpected indent")
		case "dedent":
			pyFail(c.peek().line, "unexpected dedent")
		default:
			c.parseStmt()
		}
	}
}

// pyUnsupported names the constructs this frontend refuses by name, so a
// player sees "import is not supported" rather than a generic parse error.
var pyUnsupported = map[string]string{
	"def":      "functions are not supported",
	"class":    "classes are not supported",
	"import":   "import is not supported",
	"from":     "import is not supported",
	"for":      "for is not supported (use while)",
	"lambda":   "lambda is not supported",
	"yield":    "yield is not supported",
	"with":     "with is not supported",
	"try":      "try/except is not supported",
	"global":   "global is not supported",
	"nonlocal": "nonlocal is not supported",
	"assert":   "assert is not supported",
	"del":      "del is not supported",
	"raise":    "raise is not supported",
	"pass":     "pass is not supported",
	"break":    "break is not supported",
	"continue": "continue is not supported",
	"return":   "return is not supported (no functions)",
	"input":    "input is not supported",
}

func (c *pyCompiler) parseStmt() {
	t := c.peek()
	if t.kind != "ident" {
		pyFail(t.line, "unknown statement")
	}
	switch t.val {
	case "print":
		c.parsePrint()
		return
	case "if":
		c.parseIf()
		return
	case "while":
		c.parseWhile()
		return
	}
	if msg, bad := pyUnsupported[t.val]; bad {
		pyFail(t.line, "%s", msg)
	}
	// an assignment is the only remaining statement shape: NAME = expr
	if nx := c.toks[min(c.pos+1, len(c.toks)-1)]; nx.kind == "op" && nx.val == "=" {
		c.parseAssign()
		return
	}
	pyFail(t.line, "unknown statement %q", t.val)
}

func (c *pyCompiler) parseAssign() {
	nameTok := c.next()
	c.expectOp("=")
	reg := c.parseExpr()
	c.expectNL()
	vr := c.defineVar(nameTok.val, nameTok.line)
	// copy the value into the variable's register: a self-referencing rhs
	// (x = x + 1) must be fully evaluated before x is overwritten
	c.emit(BCInstr{Op: "LOAD", A: vr, B: 0, J: -1})
	c.emit(BCInstr{Op: "ADD", A: vr, B: vr, C: reg, J: -1})
	c.freeTemp(reg)
}

func (c *pyCompiler) parsePrint() {
	c.next() // print
	c.expectOp("(")
	if t := c.peek(); t.kind == "str" {
		c.next()
		c.expectOp(")")
		c.expectNL()
		c.emit(BCInstr{Op: "PRINTS", S: t.val, J: -1})
		return
	}
	reg := c.parseExpr()
	c.expectOp(")")
	c.expectNL()
	c.emit(BCInstr{Op: "PRINTR", A: reg, J: -1})
	c.freeTemp(reg)
}

func (c *pyCompiler) parseBlock() {
	c.expectNL()
	if c.peek().kind != "indent" {
		pyFail(c.peek().line, "expected an indented block")
	}
	c.next() // indent
	for c.peek().kind != "dedent" {
		switch c.peek().kind {
		case "eof":
			pyFail(c.peek().line, "unexpected end of file (block not closed)")
		case "nl":
			c.next()
		case "indent":
			pyFail(c.peek().line, "unexpected indent")
		default:
			c.parseStmt()
		}
	}
	c.next() // dedent
}

// parseCondJump compiles `expr CMP expr :` and returns the index of the jump
// taken when the condition is FALSE (so a body naturally follows it).
func (c *pyCompiler) parseCondJump() int {
	left := c.parseExpr()
	opTok := c.next()
	if opTok.kind != "op" {
		pyFail(opTok.line, "expected comparison operator")
	}
	var jump string
	switch opTok.val {
	case "==":
		jump = "JNZ" // not equal -> skip
	case "!=":
		jump = "JZ" // equal -> skip
	case "<":
		jump = "JGE"
	case "<=":
		jump = "JGT"
	case ">":
		jump = "JLE"
	case ">=":
		jump = "JLT"
	default:
		pyFail(opTok.line, "expected comparison operator, got %q", opTok.val)
	}
	right := c.parseExpr()
	c.expectOp(":")
	scratch := c.allocTemp(opTok.line)
	c.emit(BCInstr{Op: "CMP", A: scratch, B: left, C: right, J: -1})
	j := c.emit(BCInstr{Op: jump, J: -1})
	c.freeTemp(scratch)
	c.freeTemp(right)
	c.freeTemp(left)
	return j
}

func (c *pyCompiler) parseIf() {
	c.next() // if
	condJump := c.parseCondJump()
	var endJumps []int
	for {
		c.parseBlock()
		endJumps = append(endJumps, c.emit(BCInstr{Op: "JMP", J: -1}))
		t := c.peek()
		if t.kind == "ident" && t.val == "elif" {
			c.patch(condJump, c.here())
			c.next()
			condJump = c.parseCondJump()
			continue
		}
		if t.kind == "ident" && t.val == "else" {
			c.patch(condJump, c.here())
			c.next()
			c.expectOp(":")
			c.parseBlock()
			break
		}
		c.patch(condJump, c.here())
		break
	}
	end := c.here()
	for _, j := range endJumps {
		c.patch(j, end)
	}
}

func (c *pyCompiler) parseWhile() {
	c.next() // while
	start := c.here()
	condJump := c.parseCondJump()
	c.parseBlock()
	c.emit(BCInstr{Op: "JMP", J: start})
	c.patch(condJump, c.here())
}

// ---- expressions -----------------------------------------------------------

func (c *pyCompiler) parseExpr() int { return c.parseAdd() }

func (c *pyCompiler) parseAdd() int {
	r := c.parseMul()
	for {
		t := c.peek()
		if t.kind != "op" || (t.val != "+" && t.val != "-") {
			return r
		}
		c.next()
		op := "ADD"
		if t.val == "-" {
			op = "SUB"
		}
		t2 := c.parseMul()
		c.emit(BCInstr{Op: op, A: r, B: r, C: t2, J: -1})
		c.freeTemp(t2)
	}
}

func (c *pyCompiler) parseMul() int {
	r := c.parseUnary()
	for {
		t := c.peek()
		if t.kind != "op" || (t.val != "*" && t.val != "/" && t.val != "%") {
			return r
		}
		c.next()
		op := "MUL"
		if t.val == "/" {
			op = "DIV" // integer division, the documented subset limit
		} else if t.val == "%" {
			op = "MOD"
		}
		t2 := c.parseUnary()
		c.emit(BCInstr{Op: op, A: r, B: r, C: t2, J: -1})
		c.freeTemp(t2)
	}
}

func (c *pyCompiler) parseUnary() int {
	if t := c.peek(); t.kind == "op" && t.val == "-" {
		c.next()
		r := c.parseUnary()
		zero := c.allocTemp(t.line)
		c.emit(BCInstr{Op: "LOAD", A: zero, B: 0, J: -1})
		c.emit(BCInstr{Op: "SUB", A: r, B: zero, C: r, J: -1})
		c.freeTemp(zero)
		return r
	}
	return c.parseAtom()
}

// pyUnsupportedAtom are names that look like expression atoms but are not in
// the subset; refusing them by name is friendlier than "undefined name".
var pyUnsupportedAtom = map[string]string{
	"True":  "booleans are not supported",
	"False": "booleans are not supported",
	"None":  "None is not supported",
	"input": "input is not supported",
	"int":   "int() is not supported (values are already integers)",
	"str":   "str() is not supported (strings print only)",
	"len":   "len() is not supported",
	"range": "range() is not supported (use while)",
	"float": "floats are not supported",
	"and":   "and/or/not are not supported",
	"or":    "and/or/not are not supported",
	"not":   "and/or/not are not supported",
	"in":    "in is not supported",
	"is":    "is is not supported",
}

func (c *pyCompiler) parseAtom() int {
	t := c.next()
	switch t.kind {
	case "int":
		n, err := strconv.Atoi(t.val)
		if err != nil {
			pyFail(t.line, "integer literal out of range")
		}
		r := c.allocTemp(t.line)
		c.emit(BCInstr{Op: "LOAD", A: r, B: n, J: -1})
		return r
	case "ident":
		if msg, bad := pyUnsupportedAtom[t.val]; bad {
			pyFail(t.line, "%s", msg)
		}
		vr, ok := c.vars[t.val]
		if !ok {
			pyFail(t.line, "undefined name %q", t.val)
		}
		r := c.allocTemp(t.line)
		// there is no mem->reg load, so a variable copy is 0 + var
		c.emit(BCInstr{Op: "LOAD", A: r, B: 0, J: -1})
		c.emit(BCInstr{Op: "ADD", A: r, B: r, C: vr, J: -1})
		return r
	case "op":
		if t.val == "(" {
			r := c.parseExpr()
			c.expectOp(")")
			return r
		}
		pyFail(t.line, "bad expression")
	case "str":
		pyFail(t.line, "string literals are only supported in print")
	}
	pyFail(t.line, "bad expression")
	return 0
}
