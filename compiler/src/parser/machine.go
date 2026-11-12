package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/machineroots"
	"strings"
)

// docs/123 — `machine over INPUT [while COND] [-> YIELD]:` checked state-machine loops.
//
// A machine is a RESTRICTION construct: a loop whose body is a single dispatch over a
// declared, typed state, where every arm is exactly one (state, input) decision ending in
// exactly one transition (`-> State(args)`) or exit (`return`/`break`). The compiler
// refuses everything else (docs/123 §5): no branching in arm bodies, no arm without a
// decision, no foreign mutation, per-state exhaustiveness, no unreachable arms, no
// undeclared transition targets.
//
// It desugars — entirely in the parser, onto existing nodes — to the exact shape the
// docs/121 read_fstring pilot validated bit-exact and zero-overhead:
//
//   - a hoisted file-scope payload-less mode enum (one variant per state),
//   - SCALARIZED payloads: each payload field becomes one loop-header mutable local
//     (shared across states that declare the same field name+type — matching the
//     pilot's single `depth` accumulator),
//   - `while COND |captures, mode = Start, fields…|:` via the docs/119 loopHeader
//     machinery (wrapLoopHeader), so scoping/licensing/analysis reuse the tested paths,
//   - a fresh `input = INPUT` read at the top of each step,
//   - one `match mode:` whose per-state arm is the arm chain lowered to a single
//     `if`/`elif`/`else` ladder (payload-pattern cond AND input cond AND guard),
//   - `-> State(args)` lowered to payload assigns + `mode <- Enum.State` rebinds
//     (self-state mode rebinds and `field <- field` no-op assigns are elided).
//
// Scalarizing payloads (rather than enum payloads) is load-bearing twice over: enum
// payload decls carry no `where` refinements in stage0, and the payload-less mode enum +
// scalar accumulators is the pilot's exact bit-pattern, so zero overhead is inherited
// rather than re-proven.

type machineField struct {
	pos      lexer.Pos
	name     string
	typ      ast.TypeExpr
	typeText string // raw source spelling, for cross-state same-name compatibility checks
}

type machineState struct {
	pos    lexer.Pos
	name   string
	fields []machineField
}

// machinePayloadPat is one payload-argument pattern in an arm header. Exactly one of the
// three forms: wildcard `_`, a plain-identifier bind (aliasing the field's scalar local),
// or a condition expression (a literal means `field == lit`; an expression mentioning the
// field name is used verbatim as the condition, e.g. `depth > 1`).
type machinePayloadPat struct {
	pos  lexer.Pos
	wild bool
	bind string   // non-empty for a plain-ident bind
	cond ast.Expr // non-nil for the condition form
}

type machineExitKind int

const (
	machineExitNone machineExitKind = iota
	machineExitTransition
	machineExitReturn
	machineExitBreak
)

type machineArm struct {
	pos               lexer.Pos
	state             string
	payload           []machinePayloadPat
	inputs            []ast.Expr          // literal alternatives (`'a' | 'b'`); nil when inputWild
	inputRanges       []machineInputRange // range alternatives (`'0'..='9'`), OR'd with inputs
	inputWild         bool
	inputBind         string // input bind pattern: names the input value for guard/body
	guard             ast.Expr
	body              []ast.Stmt // statements before the exit; excludes the transition line
	exit              machineExitKind
	target            string // transition target state
	targetPos         lexer.Pos
	nameSuffix        string               // unique per arm; names the arm's transition temporaries
	args              []ast.Expr           // transition payload args
	branchTransitions map[lexer.Pos]string // branch-local `-> State` markers, represented as BreakStmt until lowering
	branchExit        bool                 // the parsed conditional has no paths left for a following arm statement
}

// machineInputRange is a range alternative in a machine arm header (`Num, '0'..='9':`),
// shared with the docs/122 pattern grammar. It lowers to a bounds test on the input var
// (`lo <= input and input <(=) hi`), OR'd with the arm's literal alternatives.
type machineInputRange struct {
	pos       lexer.Pos
	lo        ast.Expr
	hi        ast.Expr
	inclusive bool
}

// machineForHeader is the optional iterator driver in
// `machine over value for pattern in iterable:`.  Keeping it as an ordinary
// for-loop AST node lets the existing range/iterator lowering do the driving;
// the machine contributes only the typed state dispatch in its body.
type machineForHeader struct {
	pos      lexer.Pos
	mode     ast.IterBindMode
	pattern  ast.MoveBindPattern
	source   ast.Expr
	rangeEnd ast.Expr
	rangeOp  lexer.TokenKind
}

// looksLikeMachineStmt gates the contextual keyword: `machine` begins a machine statement
// only when followed by the ident `over` — no legal expression puts two bare identifiers
// in sequence, so a variable named `machine` still parses normally.
func (p *Parser) looksLikeMachineStmt() bool {
	return p.hasToken(p.pos+1) && p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IDENT && p.tokenAt(p.pos+1).Text == "over"
}

func (p *Parser) parseMachineStmt() ast.Stmt {
	pos := p.cur().Pos
	startToken := p.pos
	p.advance() // machine
	p.expectIdentText("over")
	// The header expressions are sub-parsed to their clause boundary: a top-level `->`
	// would otherwise be eaten by the expression grammar as the removed `expr -> T` cast.
	input := p.parseMachineHeaderExpr(true)
	var forHeader *machineForHeader
	if p.peekIdentText("for") {
		forHeader = p.parseMachineForHeader()
	}
	var cond ast.Expr
	if p.peek() == lexer.TOKEN_WHILE {
		p.advance()
		cond = p.parseMachineHeaderExpr(false)
	}
	var yield ast.Expr
	if p.match(lexer.TOKEN_ARROW) {
		yield = p.parseMachineHeaderExpr(false)
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)

	var states []machineState
	var startState string
	var startArgs []ast.Expr
	startSeen := false
	var arms []machineArm

	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() != lexer.TOKEN_IDENT {
			p.errorf("machine body expects `state`, `start`, or a `State, input:` arm")
			p.advance()
			continue
		}
		switch p.cur().Text {
		case "state":
			if len(arms) > 0 || startSeen {
				p.errorf("machine `state` declarations must precede `start` and the arms")
			}
			states = append(states, p.parseMachineStateDecls()...)
			continue
		case "start":
			p.advance()
			if startSeen {
				p.errorf("machine has more than one `start`")
			}
			startSeen = true
			startState = p.expect(lexer.TOKEN_IDENT).Text
			if p.match(lexer.TOKEN_LPAREN) {
				if p.peek() != lexer.TOKEN_RPAREN {
					for {
						startArgs = append(startArgs, p.parseExpr())
						if !p.match(lexer.TOKEN_COMMA) {
							break
						}
					}
				}
				p.expect(lexer.TOKEN_RPAREN)
			}
			p.expectNewline()
			continue
		}
		if !startSeen {
			p.errorf("machine arms must follow the `start` declaration")
			startSeen = true // report once, keep parsing arms
		}
		arms = append(arms, p.parseMachineArm(states))
	}
	p.expect(lexer.TOKEN_DEDENT)

	suffix := p.contentNameSuffix(pos, startToken, p.pos)
	return p.desugarMachine(pos, suffix, input, cond, yield, forHeader, states, startState, startArgs, startSeen, arms)
}

func (p *Parser) parseMachineForHeader() *machineForHeader {
	pos := p.advance().Pos // contextual `for`
	mode := p.parseIterBindMode()
	pattern := p.parseIterLoopPattern()
	p.expect(lexer.TOKEN_IN)
	source := p.parseMachineForSource()
	h := &machineForHeader{pos: pos, mode: mode, pattern: pattern, source: source}
	if p.peek() == lexer.TOKEN_RANGE || p.peek() == lexer.TOKEN_RANGE_LT || p.peek() == lexer.TOKEN_RANGE_GT || p.peek() == lexer.TOKEN_RANGE_LE {
		op := p.advance()
		h.rangeOp = op.Kind
		h.rangeEnd = p.parseMachineHeaderExpr(false)
	}
	return h
}

func (p *Parser) parseMachineForSource() ast.Expr {
	end := p.pos
	depth := 0
	for p.hasToken(end) {
		kind := p.tokenAt(end).Kind
		switch kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				return p.parseForHeaderSlice(end, p.tokenAtOrLast(end).Pos)
			}
		}
		end++
	}
	return p.parseForHeaderSlice(end, p.tokenAtOrLast(end).Pos)
}

// parseMachineHeaderExpr sub-parses one machine-header clause expression, bounded at the
// first top-level `:`, `->`, newline — and, when stopAtWhile is set, the `while` that
// introduces the loop-condition clause.
func (p *Parser) parseMachineHeaderExpr(stopAtWhile bool) ast.Expr {
	end := p.pos
	depth := 0
headerScan:
	for p.hasToken(end) {
		switch p.tokenAt(end).Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_COLON, lexer.TOKEN_ARROW, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				break headerScan
			}
		case lexer.TOKEN_WHILE:
			if depth == 0 && stopAtWhile {
				break headerScan
			}
		case lexer.TOKEN_IDENT:
			if depth == 0 && stopAtWhile && p.tokenAt(end).Text == "for" {
				break headerScan
			}
		}
		end++
	}
	return p.parseForHeaderSlice(end, p.tokenAtOrLast(end).Pos)
}

func (p *Parser) parseMachineStateDecl() machineState {
	p.advance() // state
	nameTok := p.expect(lexer.TOKEN_IDENT)
	st := machineState{pos: nameTok.Pos, name: nameTok.Text}
	if p.match(lexer.TOKEN_LPAREN) {
		for {
			fTok := p.expect(lexer.TOKEN_IDENT)
			p.expect(lexer.TOKEN_COLON)
			typeStart := p.pos
			// `usize where depth > 0` parses as a refinement TYPE — the scalarized payload
			// local carries it, so transition edges are refinement-checked by the existing
			// typed-local machinery (docs/123 §5.7) with no machine-specific discharge.
			typ := p.parseTypeExpr()
			typeText := p.tokenTextSpan(typeStart, p.pos)
			st.fields = append(st.fields, machineField{pos: fTok.Pos, name: fTok.Text, typ: typ, typeText: typeText})
			if !p.match(lexer.TOKEN_COMMA) {
				break
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expectNewline()
	return st
}

// parseMachineStateDecls accepts either one typed declaration (`state Name(payload: T)`)
// or a concise group of payload-less declarations (`state {A, B, C}`). The grouped form
// is declaration sugar only; downstream machine validation sees the same state slice.
func (p *Parser) parseMachineStateDecls() []machineState {
	if !p.hasToken(p.pos+1) || p.tokenAt(p.pos+1).Kind != lexer.TOKEN_LBRACE {
		return []machineState{p.parseMachineStateDecl()}
	}
	p.advance() // state
	p.expect(lexer.TOKEN_LBRACE)
	states := make([]machineState, 0, 4)
	for p.peek() != lexer.TOKEN_RBRACE && p.peek() != lexer.TOKEN_EOF {
		nameTok := p.expect(lexer.TOKEN_IDENT)
		states = append(states, machineState{pos: nameTok.Pos, name: nameTok.Text})
		if !p.match(lexer.TOKEN_COMMA) {
			break
		}
	}
	p.expect(lexer.TOKEN_RBRACE)
	p.expectNewline()
	return states
}

// tokenTextSpan joins the raw text of tokens[start:end) — used to compare type spellings
// of same-named payload fields across states without needing a TypeExpr unparser.
func (p *Parser) tokenTextSpan(start, end int) string {
	var b strings.Builder
	for i := start; i < end && p.hasToken(i); i++ {
		b.WriteString(p.tokenAt(i).Text)
	}
	return b.String()
}

func (p *Parser) parseMachineArm(states []machineState) machineArm {
	stateTok := p.expect(lexer.TOKEN_IDENT)
	arm := machineArm{pos: stateTok.Pos, state: stateTok.Text}
	if p.match(lexer.TOKEN_LPAREN) {
		if p.peek() != lexer.TOKEN_RPAREN {
			for {
				arm.payload = append(arm.payload, p.parseMachinePayloadPat())
				if !p.match(lexer.TOKEN_COMMA) {
					break
				}
			}
		}
		p.expect(lexer.TOKEN_RPAREN)
	}
	p.expect(lexer.TOKEN_COMMA)
	switch {
	case p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_":
		p.advance()
		arm.inputWild = true
	case p.peek() == lexer.TOKEN_IDENT && p.hasToken(p.pos+1) &&
		(p.tokenAt(p.pos+1).Kind == lexer.TOKEN_IF || p.tokenAt(p.pos+1).Kind == lexer.TOKEN_COLON):
		// Input BIND pattern: `Scanning, character if character.is_identifier_char():` —
		// matches any input (like `_`) and names it for the guard/body, so call-predicate
		// dispatch reads the input once. A qualified member (`TokenKind.Ident`) has a `.`
		// after the ident and takes the literal path below instead.
		arm.inputBind = p.advance().Text
		arm.inputWild = true
	default:
		p.parseMachineInputAlt(&arm)
		for p.match(lexer.TOKEN_PIPE) {
			p.parseMachineInputAlt(&arm)
		}
	}
	if p.peek() == lexer.TOKEN_IF {
		p.advance()
		arm.guard = p.parseExpr()
	}
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	p.parseMachineArmBody(&arm)
	p.expect(lexer.TOKEN_DEDENT)
	return arm
}

// parseMachineInputAlt parses one input alternative in a machine arm header: a literal (or
// literal-valued expression), or a `LO..<HI` / `LO..=HI` range (docs/122 §5.2 range
// pattern, shared with match/when). The spelling matches match arms — a bare `..` is
// diagnosed toward the explicit bounds. Ranges are collected separately and OR'd into the
// arm's input condition alongside the equality alternatives.
func (p *Parser) parseMachineInputAlt(arm *machineArm) {
	pos := p.cur().Pos
	lo := p.parseMatchValuePatternExpr()
	switch p.peek() {
	case lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_LE:
		inclusive := p.advance().Kind == lexer.TOKEN_RANGE_LE
		arm.inputRanges = append(arm.inputRanges, machineInputRange{pos: pos, lo: lo, hi: p.parseMatchValuePatternExpr(), inclusive: inclusive})
	case lexer.TOKEN_RANGE:
		p.errorf("machine arm range needs an explicit bound spelling: use `..<` (exclusive) or `..=` (inclusive)")
		p.advance()
		arm.inputRanges = append(arm.inputRanges, machineInputRange{pos: pos, lo: lo, hi: p.parseMatchValuePatternExpr(), inclusive: true})
	default:
		arm.inputs = append(arm.inputs, lo)
	}
}

// parseMachinePayloadPat parses one payload-argument pattern: `_`, a plain-ident bind, or
// a condition expression (sub-parsed to the enclosing `,`/`)` boundary so the expression
// grammar cannot eat the argument separator).
func (p *Parser) parseMachinePayloadPat() machinePayloadPat {
	pos := p.cur().Pos
	if p.peek() == lexer.TOKEN_IDENT && p.cur().Text == "_" {
		p.advance()
		return machinePayloadPat{pos: pos, wild: true}
	}
	end := p.pos
	depth := 0
argScan:
	for p.hasToken(end) {
		switch p.tokenAt(end).Kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth == 0 {
				break argScan
			}
			depth--
		case lexer.TOKEN_COMMA, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				break argScan
			}
		}
		end++
	}
	expr := p.parseForHeaderSlice(end, p.tokenAtOrLast(end).Pos)
	if id, ok := expr.(*ast.Ident); ok {
		return machinePayloadPat{pos: pos, bind: id.Name}
	}
	return machinePayloadPat{pos: pos, cond: expr}
}

// parseMachineArmBody parses straight-line statements and conditional paths. Shared
// fallthrough statements may follow a partial branch; the complete arm must end in one
// transition, return, or break on every path.
func (p *Parser) parseMachineArmBody(arm *machineArm) {
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if arm.exit != machineExitNone || arm.branchExit {
			p.errorf("machine arm continues after its `->`/return/break decision — the decision must be the arm's final statement")
			// consume the rest of the arm so parsing resumes at the DEDENT
			for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
				p.advance()
			}
			break
		}
		if p.peek() == lexer.TOKEN_ARROW {
			arrowTok := p.advance()
			arm.exit = machineExitTransition
			arm.targetPos = arrowTok.Pos
			arm.target = p.expect(lexer.TOKEN_IDENT).Text
			if p.match(lexer.TOKEN_LPAREN) {
				if p.peek() != lexer.TOKEN_RPAREN {
					for {
						arm.args = append(arm.args, p.parseExpr())
						if !p.match(lexer.TOKEN_COMMA) {
							break
						}
					}
				}
				p.expect(lexer.TOKEN_RPAREN)
			}
			p.expectNewline()
			continue
		}
		if p.peek() == lexer.TOKEN_IF {
			if arm.exit != machineExitNone || arm.branchExit {
				p.errorf("machine arm continues after its branch decision")
				break
			}
			branch := p.parseMachineBranch(arm)
			arm.body = append(arm.body, branch)
			arm.branchExit = !machineroots.CanFallThrough([]ast.Stmt{branch}, arm.branchTransitions)
			p.validateMachineArmStmt(branch, arm)
			continue
		}
		stmt := p.parseStmt()
		// A desugaring inside the arm (e.g. `ghost:`) may buffer extra flat statements.
		if len(p.pendingStmts) > 0 {
			extra := p.pendingStmts
			p.pendingStmts = nil
			p.validateMachineArmStmt(stmt, arm)
			arm.body = append(arm.body, stmt)
			for _, s := range extra {
				p.validateMachineArmStmt(s, arm)
				arm.body = append(arm.body, s)
			}
			continue
		}
		if stmt == nil {
			continue
		}
		p.validateMachineArmStmt(stmt, arm)
		arm.body = append(arm.body, stmt)
	}
}

// parseMachineBranch parses one conditional branch. A path may end in a transition, return,
// or break, or fall through to a shared suffix. The temporary BreakStmt transition markers
// are replaced during lowering while retaining the source branch structure.
func (p *Parser) parseMachineBranch(arm *machineArm) ast.Stmt {
	pos := p.expect(lexer.TOKEN_IF).Pos
	cond := p.parseExpr()
	p.expect(lexer.TOKEN_COLON)
	p.expectNewline()
	p.expect(lexer.TOKEN_INDENT)
	thenBody := p.parseMachineBranchBody(arm)
	p.expect(lexer.TOKEN_DEDENT)
	var elifs []ast.ElifClause
	for p.peek() == lexer.TOKEN_ELIF {
		elifPos := p.advance().Pos
		elifCond := p.parseExpr()
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		elifBody := p.parseMachineBranchBody(arm)
		p.expect(lexer.TOKEN_DEDENT)
		elifs = append(elifs, ast.ElifClause{Position: elifPos, Cond: elifCond, Body: elifBody})
	}
	var elseBody []ast.Stmt
	if p.match(lexer.TOKEN_ELSE) {
		p.expect(lexer.TOKEN_COLON)
		p.expectNewline()
		p.expect(lexer.TOKEN_INDENT)
		elseBody = p.parseMachineBranchBody(arm)
		p.expect(lexer.TOKEN_DEDENT)
	}
	branch := &ast.IfStmt{Position: pos, Cond: cond, Then: thenBody, Elifs: elifs, Else: elseBody, FromSource: true}
	if machineroots.HasBranchMarker(branch, arm.branchTransitions) && machineroots.HasFallthroughLocalDecl(branch, arm.branchTransitions) {
		p.errorf("a declaration in a fallthrough branch cannot share a continuation with a branch transition")
	}
	return branch
}
