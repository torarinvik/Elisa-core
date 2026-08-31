package parser

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/lexer"
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
	pos         lexer.Pos
	state       string
	payload     []machinePayloadPat
	inputs      []ast.Expr          // literal alternatives (`'a' | 'b'`); nil when inputWild
	inputRanges []machineInputRange // range alternatives (`'0'..='9'`), OR'd with inputs
	inputWild   bool
	inputBind   string // input bind pattern: names the input value for guard/body
	guard       ast.Expr
	body        []ast.Stmt // statements before the exit; excludes the transition line
	exit        machineExitKind
	target      string // transition target state
	targetPos   lexer.Pos
	args        []ast.Expr // transition payload args
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
	return p.pos+1 < len(p.tokens) &&
		p.tokens[p.pos+1].Kind == lexer.TOKEN_IDENT &&
		p.tokens[p.pos+1].Text == "over"
}

func (p *Parser) parseMachineStmt() ast.Stmt {
	pos := p.cur().Pos
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

	return p.desugarMachine(pos, input, cond, yield, forHeader, states, startState, startArgs, startSeen, arms)
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
	for end < len(p.tokens) {
		kind := p.tokens[end].Kind
		switch kind {
		case lexer.TOKEN_LPAREN, lexer.TOKEN_LBRACKET, lexer.TOKEN_LBRACE:
			depth++
		case lexer.TOKEN_RPAREN, lexer.TOKEN_RBRACKET, lexer.TOKEN_RBRACE:
			if depth > 0 {
				depth--
			}
		case lexer.TOKEN_RANGE, lexer.TOKEN_RANGE_LT, lexer.TOKEN_RANGE_GT, lexer.TOKEN_RANGE_LE, lexer.TOKEN_COLON, lexer.TOKEN_NEWLINE, lexer.TOKEN_EOF:
			if depth == 0 {
				return p.parseForHeaderSlice(end, p.tokens[min(end, len(p.tokens)-1)].Pos)
			}
		}
		end++
	}
	return p.parseForHeaderSlice(end, p.tokens[min(end, len(p.tokens)-1)].Pos)
}

// parseMachineHeaderExpr sub-parses one machine-header clause expression, bounded at the
// first top-level `:`, `->`, newline — and, when stopAtWhile is set, the `while` that
// introduces the loop-condition clause.
func (p *Parser) parseMachineHeaderExpr(stopAtWhile bool) ast.Expr {
	end := p.pos
	depth := 0
headerScan:
	for end < len(p.tokens) {
		switch p.tokens[end].Kind {
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
			if depth == 0 && stopAtWhile && p.tokens[end].Text == "for" {
				break headerScan
			}
		}
		end++
	}
	return p.parseForHeaderSlice(end, p.tokens[min(end, len(p.tokens)-1)].Pos)
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
	if p.pos+1 >= len(p.tokens) || p.tokens[p.pos+1].Kind != lexer.TOKEN_LBRACE {
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
	for i := start; i < end && i < len(p.tokens); i++ {
		b.WriteString(p.tokens[i].Text)
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
	case p.peek() == lexer.TOKEN_IDENT && p.pos+1 < len(p.tokens) &&
		(p.tokens[p.pos+1].Kind == lexer.TOKEN_IF || p.tokens[p.pos+1].Kind == lexer.TOKEN_COLON):
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
	for end < len(p.tokens) {
		switch p.tokens[end].Kind {
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
	expr := p.parseForHeaderSlice(end, p.tokens[min(end, len(p.tokens)-1)].Pos)
	if id, ok := expr.(*ast.Ident); ok {
		return machinePayloadPat{pos: pos, bind: id.Name}
	}
	return machinePayloadPat{pos: pos, cond: expr}
}

// parseMachineArmBody parses the arm's statements and enforces the docs/123 §5 refusals
// that are visible line-by-line: no branching statements, the decision (`->`/return/break)
// is the final statement, and nothing follows it.
func (p *Parser) parseMachineArmBody(arm *machineArm) {
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if arm.exit != machineExitNone {
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

// validateMachineArmStmt enforces the per-statement refusals and records return/break
// exits. Branching statements are refused outright — including postfix guards, which
// desugar to an IfStmt: inside a machine ALL discrimination lives in the arm header.
func (p *Parser) validateMachineArmStmt(stmt ast.Stmt, arm *machineArm) {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		arm.exit = machineExitReturn
	case *ast.BreakStmt:
		arm.exit = machineExitBreak
	case *ast.ContinueStmt:
		p.errorf("machine arms cannot `continue` — every arm ends in `-> State`, `return`, or `break` (docs/123 §5)")
	case *ast.IfStmt, *ast.MatchStmt, *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt:
		p.errorf("machine arms cannot branch or loop (docs/123 §5) — move the condition into the arm header (`State, input if guard:`) or split into separate arms")
	case *ast.CanStmt:
		// A postfix `can Effect` clause wraps its statement in a CanStmt — a transparent
		// effect-licensing wrapper, not a branch. Validate the licensed statements.
		for _, inner := range s.Body {
			p.validateMachineArmStmt(inner, arm)
		}
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.AugAssignStmt, *ast.AsRefAssignStmt, *ast.ExprStmt:
		_ = s // allowed straight-line forms; mutation targets are checked at desugar time
	default:
		p.errorf("machine arms allow only straight-line statements ending in `-> State`, `return`, or `break` (docs/123 §5)")
	}
}

// --- desugar -------------------------------------------------------------------------

func (p *Parser) desugarMachine(pos lexer.Pos, input, cond, yield ast.Expr, forHeader *machineForHeader, states []machineState, startState string, startArgs []ast.Expr, startSeen bool, arms []machineArm) ast.Stmt {
	stateByName := map[string]*machineState{}
	for i := range states {
		st := &states[i]
		if _, dup := stateByName[st.name]; dup {
			p.errorf("machine state %q declared twice", st.name)
			continue
		}
		stateByName[st.name] = st
	}
	if len(states) == 0 {
		p.errorf("machine declares no states")
		return &ast.PassStmt{Position: pos}
	}
	if !startSeen {
		p.errorf("machine has no `start` declaration")
		startState = states[0].name
	}
	start, ok := stateByName[startState]
	if !ok {
		p.errorf("machine `start` names undeclared state %q", startState)
		start = &states[0]
	}
	if len(startArgs) != len(start.fields) {
		p.errorf("machine `start %s` expects %d payload argument(s), got %d", start.name, len(start.fields), len(startArgs))
	}

	// Scalarized payload locals: one per field NAME, shared across states declaring the
	// same name (the read_fstring pilot's single `depth`). A reuse with a different type
	// spelling is refused — the shared local can only have one type.
	fieldOrder := []machineField{}
	fieldByName := map[string]machineField{}
	for _, st := range states {
		seenInState := map[string]bool{}
		for _, f := range st.fields {
			if seenInState[f.name] {
				p.errorf("machine state %q repeats payload field %q", st.name, f.name)
				continue
			}
			seenInState[f.name] = true
			if prev, ok := fieldByName[f.name]; ok {
				if prev.typeText != f.typeText {
					p.errorf("machine payload field %q reused with a different type (%s vs %s) — shared payload fields must agree", f.name, prev.typeText, f.typeText)
				}
				continue
			}
			fieldByName[f.name] = f
			fieldOrder = append(fieldOrder, f)
		}
	}

	// Arm validation: declared states, payload arity, transition targets/arity, per-state
	// exhaustiveness (each state's FINAL arm must be irrefutable) and unreachable arms.
	armsByState := map[string][]int{}
	for i := range arms {
		arm := &arms[i]
		st, ok := stateByName[arm.state]
		if !ok {
			p.errorf("machine arm names undeclared state %q", arm.state)
			continue
		}
		if len(arm.payload) != len(st.fields) {
			p.errorf("machine arm %s(...) expects %d payload pattern(s), got %d", arm.state, len(st.fields), len(arm.payload))
			continue
		}
		if arm.exit == machineExitNone {
			p.errorf("machine arm %q makes no decision — end it with `-> State`, `return`, or `break` (docs/123 §5)", arm.state)
		}
		if arm.exit == machineExitTransition {
			target, ok := stateByName[arm.target]
			if !ok {
				p.errorf("machine transition `-> %s` names undeclared state", arm.target)
			} else if len(arm.args) != len(target.fields) {
				p.errorf("machine transition `-> %s` expects %d payload argument(s), got %d", arm.target, len(target.fields), len(arm.args))
			}
		}
		armsByState[arm.state] = append(armsByState[arm.state], i)
	}
	// Per-state input coverage, verified later against the input's TYPE (docs/125 §9): the
	// totality rule depends on whether the input is an open domain (char/int/float/bool/string — needs a final
	// `_`) or a closed const enum (needs all tags spelled, `_` rejected). The parser can't
	// see types, so it records coverage and a MachineCoverageStmt carries it to the analyzer.
	var coverageStates []ast.MachineCoverageState
	for _, st := range states {
		idxs := armsByState[st.name]
		if len(idxs) == 0 {
			p.errorf("machine state %q has no arms — every state must handle every input (docs/123 §5)", st.name)
			continue
		}
		cover := ast.MachineCoverageState{Position: st.pos, Name: st.name}
		seenInputs := map[string]bool{}
		hasOpenDomainInput := false
		for j, idx := range idxs {
			arm := &arms[idx]
			if machineArmHasOpenDomainInput(arm) {
				hasOpenDomainInput = true
			}
			irrefutable := machineArmIrrefutable(arm)
			if irrefutable {
				cover.HasWildcard = true
				if j != len(idxs)-1 {
					p.errorf("machine arm %d for state %q is unreachable — an earlier arm already matches every input (docs/123 §5)", j+2, st.name)
					break
				}
			}
			// Unguarded, payload-unconditional arms discharge coverage. A guard or a payload
			// condition (`Expr(depth > 1), '}':`) makes the arm conditional, so it neither
			// claims exclusive coverage of a literal (duplicate check) nor a tag (totality).
			if arm.guard == nil && machinePayloadUnconditional(arm) {
				cover.Tags = append(cover.Tags, machineArmTagNames(arm)...)
				for _, key := range machineArmInputLiteralKeys(arm) {
					if seenInputs[key] {
						p.errorf("machine arm for state %q repeats input %s — an earlier arm already handles it, so this arm is unreachable (docs/123 §5)", st.name, machineInputLiteralDisplay(key))
					} else {
						seenInputs[key] = true
					}
				}
			}
		}
		// Early Tier-1 diagnosis: an open-domain state (literal/range arms) with no `_` can
		// never be exhaustive, and the parser can prove it without types. Enum-tag states are
		// left to the coverage check, which knows the enum and its full variant set.
		if !cover.HasWildcard && hasOpenDomainInput {
			p.errorf("machine state %q does not cover all inputs — its final arm must be the unguarded wildcard `%s, _:` (docs/123 §5)", st.name, st.name)
		}
		coverageStates = append(coverageStates, cover)
	}

	// Foreign-mutation check: arm bodies may assign only to (a) roots of the driven
	// `over`/`while` expressions and (b) locals the arm itself declares. Payload fields
	// change only via `->`; anything else is foreign.
	overRoots := map[string]bool{}
	collectExprRootIdents(input, overRoots)
	if cond != nil {
		collectExprRootIdents(cond, overRoots)
	}
	mutatedRoots := map[string]bool{}
	// The input expression, while condition, and arm classifier expressions execute inside
	// the generated loop. A call in any of them can mutate a driven root just like a call in
	// an arm body, so include their call arguments and receivers in the capture manifest.
	collectMachineCallArgRoots(&ast.ExprStmt{Expr: input}, overRoots, mutatedRoots)
	collectMachineCallArgRoots(&ast.ExprStmt{Expr: cond}, overRoots, mutatedRoots)
	for i := range arms {
		armLocals := map[string]bool{}
		// Payload aliases and input binders are introduced by the arm header, so
		// they are already in scope for every statement in that arm.  Register
		// them before mutation validation; otherwise an assignment such as
		// `State(value), item: item = ...` is incorrectly reported as foreign
		// state.  A payload bind with the field's own spelling is not an alias —
		// it names the scalarized payload slot and must remain protected.
		if arms[i].inputBind != "" {
			armLocals[arms[i].inputBind] = true
		}
		if st, ok := stateByName[arms[i].state]; ok {
			for payloadIndex, pat := range arms[i].payload {
				if pat.bind == "" || payloadIndex < len(st.fields) && pat.bind == st.fields[payloadIndex].name {
					continue
				}
				armLocals[pat.bind] = true
			}
		}
		for _, arg := range arms[i].args {
			collectMachineCallArgRoots(&ast.ExprStmt{Expr: arg}, overRoots, mutatedRoots)
		}
		collectMachineCallArgRoots(&ast.ExprStmt{Expr: arms[i].guard}, overRoots, mutatedRoots)
		for _, payload := range arms[i].payload {
			collectMachineCallArgRoots(&ast.ExprStmt{Expr: payload.cond}, overRoots, mutatedRoots)
		}
		for _, literal := range arms[i].inputs {
			collectMachineCallArgRoots(&ast.ExprStmt{Expr: literal}, overRoots, mutatedRoots)
		}
		for _, inputRange := range arms[i].inputRanges {
			collectMachineCallArgRoots(&ast.ExprStmt{Expr: inputRange.lo}, overRoots, mutatedRoots)
			collectMachineCallArgRoots(&ast.ExprStmt{Expr: inputRange.hi}, overRoots, mutatedRoots)
		}
		p.checkMachineArmMutations(arms[i].body, armLocals, fieldByName, overRoots, mutatedRoots)
	}

	// --- construction ---
	suffix := machineNameSuffix(pos)
	enumName := "__MachineMode_" + suffix
	modeVar := "__machine_mode_" + suffix
	inputVar := "__machine_input_" + suffix

	variants := make([]ast.EnumVariantDecl, 0, len(states))
	for _, st := range states {
		variants = append(variants, ast.EnumVariantDecl{Position: st.pos, Name: st.name})
	}
	p.pendingDecls = append(p.pendingDecls, &ast.EnumDecl{Position: pos, Name: enumName, Variants: variants})

	enumMember := func(at lexer.Pos, state string) ast.Expr {
		return &ast.FieldExpr{Position: at, Object: &ast.Ident{Position: at, Name: enumName}, Field: state}
	}

	decls := []ast.Stmt{
		&ast.VarDeclStmt{Position: pos, Name: modeVar, Mutable: true, Value: enumMember(pos, start.name)},
	}
	startValue := map[string]ast.Expr{}
	for i, f := range start.fields {
		if i < len(startArgs) {
			startValue[f.name] = startArgs[i]
		}
	}
	for _, f := range fieldOrder {
		init := startValue[f.name]
		if init == nil {
			init = &ast.ZeroedLit{Position: f.pos}
		}
		decls = append(decls, &ast.VarDeclStmt{Position: f.pos, Name: f.name, Mutable: true, Type: f.typ, Value: init})
	}

	matchArms := make([]ast.MatchArm, 0, len(states))
	for i := range states {
		st := &states[i]
		var loweredArms []loweredMachineArm
		for _, idx := range armsByState[st.name] {
			arm := &arms[idx]
			loweredArms = append(loweredArms, p.lowerMachineArm(arm, st, stateByName, enumMember, inputVar, modeVar))
		}
		matchArms = append(matchArms, ast.MatchArm{
			Position: st.pos,
			Pattern:  &ast.MatchVariantPattern{Position: st.pos, EnumName: enumName, Variant: st.name},
			Body:     buildMachineArmChain(st.pos, loweredArms),
		})
	}

	loopCond := cond
	if loopCond == nil {
		loopCond = &ast.BoolLit{Position: pos, Value: true}
	}
	loopBody := []ast.Stmt{
		&ast.VarDeclStmt{Position: pos, Name: inputVar, Value: input},
	}
	// The coverage obligation reads the input's resolved type (docs/125 §9): it sits after
	// the input var decl so `input` is analyzed first, and carries no runtime effect.
	if len(coverageStates) > 0 {
		loopBody = append(loopBody, &ast.MachineCoverageStmt{Position: pos, Input: input, States: coverageStates})
	}
	loopBody = append(loopBody, &ast.MatchStmt{Position: pos, Value: &ast.Ident{Position: pos, Name: modeVar}, Arms: matchArms})
	var loop ast.Stmt
	if forHeader != nil {
		if forHeader.rangeEnd != nil {
			name, ok := forHeader.pattern.(*ast.MoveBindNamePattern)
			if !ok {
				p.errorf("machine range driver requires a simple loop binding")
				name = &ast.MoveBindNamePattern{Position: forHeader.pos, Name: "_"}
			}
			loop = &ast.ForStmt{Position: forHeader.pos, Name: name.Name, Start: forHeader.source, End: forHeader.rangeEnd, Op: forHeader.rangeOp, Body: loopBody}
		} else {
			loop = &ast.IterForStmt{Position: forHeader.pos, Pattern: forHeader.pattern, Mode: forHeader.mode, Source: forHeader.source, Body: loopBody}
		}
	} else {
		loop = &ast.WhileStmt{Position: pos, Cond: loopCond, Body: loopBody}
	}

	hdr := &loopHeader{pos: pos, decls: decls, captures: sortedKeys(mutatedRoots), yield: yield}
	return p.wrapLoopHeader(hdr, loop)
}

// checkMachineArmMutations enforces the foreign-mutation refusal over an arm body
// (recursing through transparent CanStmt wrappers) and records driven roots that need
// capture licensing. A driven root passed to any call may be mutated THROUGH the call
// (`advance(scanner, 1)` with a `mutable&` param), so those join the capture set too.
func (p *Parser) checkMachineArmMutations(stmts []ast.Stmt, armLocals map[string]bool, fieldByName map[string]machineField, overRoots, mutatedRoots map[string]bool) {
	checkAssignmentTarget := func(target ast.Expr) {
		root := assignRootIdent(target)
		if root == "" {
			p.errorf("machine arm assignment target is not rooted in a named binding")
			return
		}
		if armLocals[root] {
			return
		}
		if _, isField := fieldByName[root]; isField {
			p.errorf("machine arm assigns payload field %q directly — payloads change only through `-> State(args)` transitions (docs/123 §5)", root)
			return
		}
		if !overRoots[root] {
			p.errorf("machine arms may only mutate the driven resource (%s) — %q is foreign state (docs/123 §5)", strings.Join(sortedKeys(overRoots), ", "), root)
			return
		}
		mutatedRoots[root] = true
	}
	for _, stmt := range stmts {
		collectMachineCallArgRoots(stmt, overRoots, mutatedRoots)
		switch s := stmt.(type) {
		case *ast.CanStmt:
			p.checkMachineArmMutations(s.Body, armLocals, fieldByName, overRoots, mutatedRoots)
		case *ast.VarDeclStmt:
			armLocals[s.Name] = true
		case *ast.AssignStmt:
			checkAssignmentTarget(s.Target)
		case *ast.AugAssignStmt:
			checkAssignmentTarget(s.Target)
		case *ast.AsRefAssignStmt:
			checkAssignmentTarget(s.Target)
		}
	}
}

// machineArmIrrefutable reports whether an arm matches every (payload, input) with no
// guard — the per-state catch-all that discharges exhaustiveness.
func machineArmIrrefutable(arm *machineArm) bool {
	if !arm.inputWild || arm.guard != nil {
		return false
	}
	for _, pat := range arm.payload {
		if pat.cond != nil {
			return false
		}
	}
	return true
}

// machinePayloadUnconditional reports whether an arm's payload patterns impose no condition
// (every field is `_` or a plain bind), so the arm's coverage is decided entirely by its
// input alternatives. A payload condition (`Expr(depth > 1)`) means two arms sharing a
// literal are distinguished by the payload, not duplicates.
func machinePayloadUnconditional(arm *machineArm) bool {
	for _, pat := range arm.payload {
		if pat.cond != nil {
			return false
		}
	}
	return true
}

// machineArmHasOpenDomainInput reports whether an arm names any OPEN-domain input — a
// char/int/float/bool/string literal or a range. Such a state cannot be exhaustive without the
// wildcard `_`, and (unlike an enum-tag state) the parser can prove that without types, so
// a missing `_` is diagnosed early rather than deferred to the coverage check (docs/125 §9).
func machineArmHasOpenDomainInput(arm *machineArm) bool {
	if len(arm.inputRanges) > 0 {
		return true
	}
	for _, alt := range arm.inputs {
		if machineExprHasOpenDomainInput(alt) {
			return true
		}
	}
	return false
}

func machineExprHasOpenDomainInput(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.CharLit, *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit:
		return true
	case *ast.UnaryExpr:
		return machineExprHasOpenDomainInput(n.Operand)
	case *ast.ParenExpr:
		return machineExprHasOpenDomainInput(n.Inner)
	}
	return false
}

// machineArmTagNames returns the enum-member names an arm's input alternatives name, when
// they are enum-tag patterns: a qualified `Enum.Member` (FieldExpr) or a leading-dot
// `.Member` (ShorthandMemberExpr). A non-tag input (a literal or range) contributes nothing.
// Used to build the per-state tag-coverage set for the closed-enum totality check (docs/125
// §9); the caller restricts this to unguarded, payload-unconditional arms.
func machineArmTagNames(arm *machineArm) []string {
	var names []string
	for _, alt := range arm.inputs {
		switch n := alt.(type) {
		case *ast.FieldExpr:
			names = append(names, n.Field)
		case *ast.ShorthandMemberExpr:
			if len(n.Parts) > 0 {
				names = append(names, n.Parts[len(n.Parts)-1])
			}
		}
	}
	return names
}

// machineArmInputLiteralKeys returns a canonical coverage key for each of an arm's literal
// or enum-tag input alternatives (`'a'`, `7`, `1.0`, `true`, `TokenKind.Ident`). Ranges and
// non-literal expressions yield no key — duplicate detection is intentionally exact
// (0-FP): it fires only when the SAME literal is handled twice, not on possibly-overlapping
// ranges, which the analyzer cannot compare without the value domain.
func machineArmInputLiteralKeys(arm *machineArm) []string {
	var keys []string
	for _, alt := range arm.inputs {
		if k, ok := machineLiteralKey(alt); ok {
			keys = append(keys, k)
		}
	}
	return keys
}

// machineLiteralKey canonicalizes a literal / qualified-enum-member expression to a stable
// string key, returning ok=false for anything else.
func machineLiteralKey(e ast.Expr) (string, bool) {
	switch n := e.(type) {
	case *ast.CharLit:
		return "char:" + n.Value, true
	case *ast.IntLit:
		if n.IsHex {
			return "int:hex:" + n.Value, true
		}
		return "int:" + n.Value, true
	case *ast.FloatLit:
		return "float:" + n.Value, true
	case *ast.StringLit:
		return "str:" + n.Value, true
	case *ast.BoolLit:
		if n.Value {
			return "bool:true", true
		}
		return "bool:false", true
	case *ast.UnaryExpr:
		if key, ok := machineLiteralKey(n.Operand); ok {
			// The machine-value grammar currently admits only unary minus here. Keep the
			// operator out of the key: the operand key is already distinct from every
			// direct literal key, and this avoids depending on TokenKind stringification.
			return "unary:" + key, true
		}
	case *ast.ParenExpr:
		return machineLiteralKey(n.Inner)
	case *ast.FieldExpr:
		// A qualified enum member `Enum.Variant` (classified-dispatch tags).
		if obj, ok := n.Object.(*ast.Ident); ok {
			return "tag:" + obj.Name + "." + n.Field, true
		}
	case *ast.ShorthandMemberExpr:
		// A leading-dot enum member `.Variant` (classified-dispatch tag). Keep the
		// complete shorthand path so repeated spellings are rejected exactly without
		// conflating it with a qualified member from an unrelated enum.
		if len(n.Parts) > 0 {
			return "tag:." + strings.Join(n.Parts, "."), true
		}
	}
	return "", false
}

// machineInputLiteralDisplay renders a coverage key back to a readable form for diagnostics.
func machineInputLiteralDisplay(key string) string {
	switch {
	case strings.HasPrefix(key, "char:"):
		return "'" + strings.TrimPrefix(key, "char:") + "'"
	case strings.HasPrefix(key, "int:hex:"):
		return strings.TrimPrefix(key, "int:hex:")
	case strings.HasPrefix(key, "int:"):
		return strings.TrimPrefix(key, "int:")
	case strings.HasPrefix(key, "float:"):
		return strings.TrimPrefix(key, "float:")
	case strings.HasPrefix(key, "unary:"):
		return "-" + machineInputLiteralDisplay(strings.TrimPrefix(key, "unary:"))
	case strings.HasPrefix(key, "str:"):
		return "\"" + strings.TrimPrefix(key, "str:") + "\""
	case strings.HasPrefix(key, "bool:"):
		return strings.TrimPrefix(key, "bool:")
	case strings.HasPrefix(key, "tag:"):
		return strings.TrimPrefix(key, "tag:")
	}
	return key
}

type loweredMachineArm struct {
	pos  lexer.Pos
	cond ast.Expr // nil for the irrefutable arm
	body []ast.Stmt
}

// lowerMachineArm builds one arm's dispatch condition and lowered body.
func (p *Parser) lowerMachineArm(arm *machineArm, st *machineState, stateByName map[string]*machineState, enumMember func(lexer.Pos, string) ast.Expr, inputVar, modeVar string) loweredMachineArm {
	andJoin := func(a, b ast.Expr) ast.Expr {
		if a == nil {
			return b
		}
		if b == nil {
			return a
		}
		return &ast.BinaryExpr{Position: arm.pos, Op: lexer.TOKEN_AND, Left: a, Right: b}
	}

	var cond ast.Expr
	var aliasDecls []ast.Stmt
	for i, pat := range arm.payload {
		if i >= len(st.fields) {
			break
		}
		field := st.fields[i]
		switch {
		case pat.wild:
		case pat.bind != "":
			if pat.bind != field.name {
				aliasDecls = append(aliasDecls, &ast.VarDeclStmt{Position: pat.pos, Name: pat.bind, Value: &ast.Ident{Position: pat.pos, Name: field.name}})
			}
		case pat.cond != nil:
			if exprMentionsIdent(pat.cond, field.name) {
				// `Expr(depth > 1)` — the expression IS the condition over the scalar local.
				cond = andJoin(cond, pat.cond)
			} else {
				// `Expr(1)` — a literal payload pattern is an equality test.
				cond = andJoin(cond, &ast.BinaryExpr{Position: pat.pos, Op: lexer.TOKEN_EQEQ, Left: &ast.Ident{Position: pat.pos, Name: field.name}, Right: pat.cond})
			}
		}
	}
	if !arm.inputWild {
		var inputCond ast.Expr
		orIn := func(c ast.Expr) {
			if inputCond == nil {
				inputCond = c
			} else {
				inputCond = &ast.BinaryExpr{Position: arm.pos, Op: lexer.TOKEN_OR, Left: inputCond, Right: c}
			}
		}
		for _, alt := range arm.inputs {
			orIn(&ast.BinaryExpr{Position: arm.pos, Op: lexer.TOKEN_EQEQ, Left: &ast.Ident{Position: arm.pos, Name: inputVar}, Right: alt})
		}
		for _, r := range arm.inputRanges {
			// `lo <= input and input <(=) hi` — the input var is read twice, but it is a
			// plain synthesized local (the classified enum tag / current char), so there is
			// no re-evaluation cost.
			hiOp := lexer.TOKEN_LT
			if r.inclusive {
				hiOp = lexer.TOKEN_LTEQ
			}
			lower := &ast.BinaryExpr{Position: r.pos, Op: lexer.TOKEN_LTEQ, Left: r.lo, Right: &ast.Ident{Position: r.pos, Name: inputVar}}
			upper := &ast.BinaryExpr{Position: r.pos, Op: hiOp, Left: &ast.Ident{Position: r.pos, Name: inputVar}, Right: r.hi}
			orIn(&ast.BinaryExpr{Position: r.pos, Op: lexer.TOKEN_AND, Left: lower, Right: upper})
		}
		cond = andJoin(cond, inputCond)
	}
	guard := arm.guard
	if guard != nil && arm.inputBind != "" {
		// The input binder is only in scope for this arm. Keep it visible while
		// evaluating the guard without hoisting it into sibling arms.
		guard = &ast.ExprBlock{
			Position: arm.pos,
			Stmts: []ast.Stmt{
				&ast.VarDeclStmt{Position: arm.pos, Name: arm.inputBind, Value: &ast.Ident{Position: arm.pos, Name: inputVar}},
			},
			Value: guard,
		}
	}
	cond = andJoin(cond, guard)

	var body []ast.Stmt
	if arm.inputBind != "" {
		// The body has a separate arm-local binder. The guard's ExprBlock above
		// has its own scope, so neither path leaks the name to a sibling arm.
		body = append(body, &ast.VarDeclStmt{Position: arm.pos, Name: arm.inputBind, Value: &ast.Ident{Position: arm.pos, Name: inputVar}})
	}
	body = append(body, aliasDecls...)
	body = append(body, arm.body...)
	if arm.exit == machineExitTransition {
		if target, ok := stateByName[arm.target]; ok {
			// The dispatch arm already has a scope of its own, but that is also the
			// scope in which arm-local declarations live.  If an arm-local shadows a
			// target payload field, a name-only backend lookup would otherwise turn
			// `-> Next(value)` into a store to the arm-local.  Put the pre-transition
			// body in one more lexical scope and emit the actual payload stores after
			// it.  Transition arguments are captured before leaving that scope so a
			// valid `-> Next(local)` keeps working as well.
			if machineArmDeclaresAnyField(body, target.fields) {
				tempDecls, captures, transition := lowerMachineTransitionAfterScope(arm, target, enumMember, modeVar)
				captures = append(body, captures...)
				body = append(tempDecls, &ast.CanStmt{Position: arm.pos, Body: captures})
				body = append(body, transition...)
			} else {
				body = append(body, lowerMachineTransition(arm, target, enumMember, modeVar)...)
			}
		}
	}
	if len(body) == 0 {
		body = append(body, &ast.PassStmt{Position: arm.pos})
	}
	return loweredMachineArm{pos: arm.pos, cond: cond, body: body}
}

// machineArmDeclaresAnyField reports whether the lowered arm body introduces a
// binding whose name is also a destination payload field.  CanStmt is the only
// transparent wrapper permitted in a source arm, but recurse through it so the
// test remains correct for `can Effect: value: T = …` as well.
func machineArmDeclaresAnyField(stmts []ast.Stmt, fields []machineField) bool {
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.VarDeclStmt:
			for _, field := range fields {
				if field.name == n.Name {
					return true
				}
			}
		case *ast.CanStmt:
			if machineArmDeclaresAnyField(n.Body, fields) {
				return true
			}
		}
	}
	return false
}

// lowerMachineTransition emits the payload assigns + mode rebind for `-> State(args)`.
// With 2+ args, argument values are captured into temps BEFORE any field assign so
// cross-referencing payloads (`-> Swap(b, a)`) read pre-transition values. Payload assigns
// are retained even when both identifiers have the same spelling: an arm-local may shadow
// the destination state slot, so `-> Next(value)` is not necessarily a no-op.
func lowerMachineTransition(arm *machineArm, target *machineState, enumMember func(lexer.Pos, string) ast.Expr, modeVar string) []ast.Stmt {
	var out []ast.Stmt
	values := arm.args
	if len(arm.args) > 1 {
		values = make([]ast.Expr, len(arm.args))
		for i, a := range arm.args {
			tmp := fmt.Sprintf("__machine_arg_%d_%d", arm.targetPos.Offset, i)
			out = append(out, &ast.VarDeclStmt{Position: arm.targetPos, Name: tmp, Value: a})
			values[i] = &ast.Ident{Position: arm.targetPos, Name: tmp}
		}
	}
	for i, f := range target.fields {
		if i >= len(values) {
			break
		}
		out = append(out, &ast.AssignStmt{Position: arm.targetPos, Target: &ast.Ident{Position: arm.targetPos, Name: f.name}, Value: values[i]})
	}
	if target.name != arm.state {
		out = append(out, &ast.AssignStmt{Position: arm.targetPos, Target: &ast.Ident{Position: arm.targetPos, Name: modeVar}, Value: enumMember(arm.targetPos, target.name)})
	}
	return out
}

// lowerMachineTransitionAfterScope splits a transition into outer temporary declarations,
// captures which run while arm locals are still in scope, and stores which run after the
// internal scope has closed.  The declarations deliberately live outside the CanStmt: a
// transition argument may refer to an arm-local, but the resulting value must survive long
// enough for the post-scope payload store to target the enclosing machine state slot.
func lowerMachineTransitionAfterScope(arm *machineArm, target *machineState, enumMember func(lexer.Pos, string) ast.Expr, modeVar string) ([]ast.Stmt, []ast.Stmt, []ast.Stmt) {
	tempDecls := make([]ast.Stmt, 0, len(arm.args))
	captures := make([]ast.Stmt, 0, len(arm.args))
	values := make([]ast.Expr, len(arm.args))
	for i, arg := range arm.args {
		name := fmt.Sprintf("__machine_arg_%s_%d", machineNameSuffix(arm.pos), i)
		var typ ast.TypeExpr
		if i < len(target.fields) {
			typ = target.fields[i].typ
		}
		tempDecls = append(tempDecls, &ast.VarDeclStmt{
			Position: arm.targetPos,
			Name:     name,
			Mutable:  true,
			Type:     typ,
			Value:    &ast.ZeroedLit{Position: arm.targetPos},
		})
		captures = append(captures, &ast.AssignStmt{Position: arm.targetPos, Target: &ast.Ident{Position: arm.targetPos, Name: name}, Value: arg})
		values[i] = &ast.Ident{Position: arm.targetPos, Name: name}
	}

	transition := make([]ast.Stmt, 0, len(target.fields)+1)
	for i, field := range target.fields {
		if i >= len(values) {
			break
		}
		transition = append(transition, &ast.AssignStmt{
			Position: arm.targetPos,
			Target:   &ast.Ident{Position: arm.targetPos, Name: field.name},
			Value:    values[i],
		})
	}
	if target.name != arm.state {
		transition = append(transition, &ast.AssignStmt{
			Position: arm.targetPos,
			Target:   &ast.Ident{Position: arm.targetPos, Name: modeVar},
			Value:    enumMember(arm.targetPos, target.name),
		})
	}
	return tempDecls, captures, transition
}

// buildMachineArmChain folds a state's lowered arms into a single if/elif/else ladder —
// or the bare body when the state has only its irrefutable arm. A state with no irrefutable
// (unguarded-wildcard) arm — legal only for a closed-enum input proven exhaustive by the
// coverage check (docs/125 §9) — gets a defensive `else: break`: dead code under a proven
// total dispatch, but a guarantee the machine can never spin on an unhandled input even if
// a hole ever slipped past the checker.
func buildMachineArmChain(pos lexer.Pos, arms []loweredMachineArm) []ast.Stmt {
	if len(arms) == 1 && arms[0].cond == nil {
		return arms[0].body
	}
	ifStmt := &ast.IfStmt{Position: arms[0].pos, Cond: arms[0].cond, Then: arms[0].body}
	hasElse := false
	for _, a := range arms[1:] {
		if a.cond == nil {
			ifStmt.Else = a.body
			hasElse = true
			break
		}
		ifStmt.Elifs = append(ifStmt.Elifs, ast.ElifClause{Position: a.pos, Cond: a.cond, Body: a.body})
	}
	if !hasElse {
		ifStmt.Else = []ast.Stmt{&ast.BreakStmt{Position: pos}}
	}
	return []ast.Stmt{ifStmt}
}

// machineNameSuffix builds a program-unique suffix for the machine's synthesized names:
// the source offset disambiguates machines within a file, and a short filename hash
// disambiguates identical offsets across files compiled into one program.
func machineNameSuffix(pos lexer.Pos) string {
	h := fnv.New32a()
	h.Write([]byte(pos.File))
	return fmt.Sprintf("%x_%d", h.Sum32(), pos.Offset)
}

// collectMachineCallArgRoots adds every driven-resource root that appears inside a call
// in the statement (as receiver or argument) to the mutated set — call-based mutation is
// invisible without types, so any driven root touching a call is conservatively licensed.
func collectMachineCallArgRoots(stmt ast.Stmt, overRoots, out map[string]bool) {
	var visit func(ast.Expr)
	var visitStatements func([]ast.Stmt)
	visitStatements = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.ExprStmt:
				visit(s.Expr)
			case *ast.AssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.AugAssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.AsRefAssignStmt:
				visit(s.Target)
				visit(s.Value)
			case *ast.VarDeclStmt:
				visit(s.Value)
			case *ast.LetDestructureStmt:
				visit(s.Value)
			case *ast.TupleBindStmt:
				visit(s.Value)
			case *ast.MoveBindStmt:
				visit(s.Value)
				visit(s.Store)
			case *ast.DeferStmt:
				visitStatements(s.Body)
			case *ast.ReturnStmt:
				visit(s.Value)
			case *ast.IfStmt:
				visit(s.Cond)
				visitStatements(s.Then)
				for _, clause := range s.Elifs {
					visit(clause.Cond)
					visitStatements(clause.Body)
				}
				visitStatements(s.Else)
			case *ast.WhileStmt:
				visit(s.Cond)
				visitStatements(s.Body)
			case *ast.ForStmt:
				visit(s.Start)
				visit(s.End)
				visit(s.Step)
				visitStatements(s.Body)
			case *ast.IterForStmt:
				visit(s.Source)
				visit(s.WhereFilter)
				visit(s.Filter)
				visitStatements(s.Body)
			case *ast.ParallelForStmt:
				visit(s.Source)
				visitStatements(s.Body)
			case *ast.MatchStmt:
				visit(s.Value)
				visit(s.Store)
				for _, arm := range s.Arms {
					visit(arm.Guard)
					visitStatements(arm.Body)
				}
			case *ast.ExpectPatternStmt:
				visit(s.Value)
			case *ast.InStoreStmt:
				visit(s.Store)
				visitStatements(s.Body)
			case *ast.CanStmt:
				for _, arg := range s.HandlerArgs {
					visit(arg)
				}
				visitStatements(s.Body)
			case *ast.ScopeStmt:
				visit(s.Guard)
				visitStatements(s.Body)
			case *ast.PoolStmt:
				visit(s.Workers)
				visitStatements(s.Body)
			case *ast.LockStmt:
				visit(s.Mutex)
				visitStatements(s.Body)
			case *ast.PanicStmt:
				visit(s.Message)
			case *ast.StaticIfStmt:
				visit(s.Cond)
				visitStatements(s.Then)
				for _, clause := range s.Elifs {
					visit(clause.Cond)
					visitStatements(clause.Body)
				}
				visitStatements(s.Else)
			case *ast.StaticErrorStmt:
				visit(s.Message)
			case *ast.StaticAssertStmt:
				visit(s.Cond)
				visit(s.Message)
			case *ast.AssertByStmt:
				visit(s.Cond)
				visitStatements(s.Proof)
			case *ast.ProofBlockStmt:
				visit(s.Goal)
				visitStatements(s.Proof)
			case *ast.ProofUseStmt:
				for _, citation := range s.Citations {
					visit(citation)
				}
			case *ast.ContractStmt:
				visit(s.Cond)
				visitStatements(s.Proof)
				for _, arg := range s.UsesArgs {
					visit(arg)
				}
			case *ast.StaticBlockStmt:
				visitStatements(s.Body)
			case *ast.DiscardStmt:
				visit(s.Value)
			case *ast.RegionStmt:
				visit(s.Capacity)
				visitStatements(s.Body)
			case *ast.PromoteStmt:
				visit(s.Value)
			case *ast.CheckpointStmt:
				visit(s.Target)
				visitStatements(s.Body)
			case *ast.GroupedCheckpointStmt:
				for _, target := range s.Targets {
					visit(target)
				}
				visitStatements(s.Body)
			}
		}
	}
	visit = func(e ast.Expr) {
		if e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.CallExpr:
			roots := map[string]bool{}
			collectExprRootIdents(n, roots)
			for r := range roots {
				if overRoots[r] {
					out[r] = true
				}
			}
		case *ast.BinaryExpr:
			visit(n.Left)
			visit(n.Right)
		case *ast.UnaryExpr:
			visit(n.Operand)
		case *ast.ParenExpr:
			visit(n.Inner)
		case *ast.FieldExpr:
			visit(n.Object)
		case *ast.IndexExpr:
			visit(n.Object)
			visit(n.Index)
			visit(n.Index2)
			visit(n.Fallback)
		case *ast.SliceExpr:
			visit(n.Object)
			visit(n.Start)
			visit(n.End)
		case *ast.CastExpr:
			visit(n.Operand)
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
			for _, key := range n.Keys {
				visit(key)
			}
			visit(n.Owner)
		case *ast.MembershipRangeExpr:
			visit(n.Start)
			visit(n.End)
		case *ast.ListComprehensionExpr:
			visit(n.Value)
			visit(n.Source)
			visit(n.RangeEnd)
			visit(n.RangeStep)
			visit(n.Filter)
			visit(n.Owner)
			visit(n.Key)
			visit(n.LoweredParallel)
			visitStatements(n.Bindings)
		case *ast.QueryExpr:
			visit(n.Source)
			visit(n.Filter)
			visit(n.Projection)
			visit(n.Owner)
		case *ast.TernaryExpr:
			visit(n.Value)
			visit(n.Cond)
			visit(n.Alt)
		case *ast.AddrOfExpr:
			visit(n.Operand)
		case *ast.SpecializeExpr:
			visit(n.Operand)
		case *ast.StructLitExpr:
			for _, arg := range n.Args {
				visit(arg)
			}
			for _, spread := range n.Spreads {
				visit(spread)
			}
		case *ast.RecordUpdateExpr:
			visit(n.Base)
			for _, arg := range n.Args {
				visit(arg)
			}
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				visit(elem)
			}
		case *ast.OptionalBindExpr:
			visit(n.Value)
		case *ast.AllocExpr:
			visit(n.Owner)
			visit(n.Value)
		case *ast.CanExpr:
			visit(n.Expr)
		case *ast.RaiseExpr:
			visit(n.Error)
		case *ast.IsPatternExpr:
			for _, target := range n.Targets {
				visit(target)
			}
		case *ast.IsAliasExpr:
			visit(n.Target)
		case *ast.ExprBlock:
			visitStatements(n.Stmts)
			visit(n.Value)
		case *ast.LambdaExpr:
			visitStatements(n.Body)
			visit(n.BodyExpr)
		case *ast.FoldExpr:
			visit(n.Value)
			for _, arm := range n.Arms {
				visit(arm.Guard)
				visitStatements(arm.Body)
			}
		case *ast.EmitExpr:
			visit(n.Value)
		case *ast.TryExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.GetExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.UnwrapElseExpr:
			visit(n.Value)
			visit(n.Fallback)
			if n.Recovery != nil {
				visit(n.Recovery.Value)
				visitStatements(n.Recovery.Body)
			}
		case *ast.CatchExpr:
			visit(n.Value)
			visitStatements(n.Success.Body)
			for _, arm := range n.Arms {
				visitStatements(arm.Body)
			}
		case *ast.MatchExpr:
			visit(n.Value)
			visit(n.Store)
			for _, arm := range n.Arms {
				visit(arm.Guard)
				visitStatements(arm.Body)
			}
		}
	}
	// Enter through the same complete statement dispatcher used for nested bodies. The
	// machine-arm validator now accepts compound and as-ref assignments, and routing only
	// plain assignments here would miss calls in their targets or values when the statement
	// is at arm level.
	visitStatements([]ast.Stmt{stmt})
}

// assignRootIdent walks an lvalue to its root identifier (`lexer.pos` → "lexer").
func assignRootIdent(target ast.Expr) string {
	for {
		switch t := target.(type) {
		case *ast.Ident:
			return t.Name
		case *ast.FieldExpr:
			target = t.Object
		case *ast.IndexExpr:
			target = t.Object
		case *ast.ParenExpr:
			target = t.Inner
		default:
			return ""
		}
	}
}

// collectExprRootIdents gathers every identifier that roots a value chain in an
// expression (`lexer.current_char()` → lexer). Used for the driven-resource whitelist.
func collectExprRootIdents(expr ast.Expr, out map[string]bool) {
	switch e := expr.(type) {
	case nil:
	case *ast.Ident:
		out[e.Name] = true
	case *ast.FieldExpr:
		collectExprRootIdents(e.Object, out)
	case *ast.CallExpr:
		// A plain-ident callee is a FUNCTION name (`at_end(scanner)`), not a value root;
		// a FieldExpr callee's object chain is a method receiver and does root a value.
		if _, plainFn := e.Func.(*ast.Ident); !plainFn {
			collectExprRootIdents(e.Func, out)
		}
		for _, a := range e.Args {
			collectExprRootIdents(a, out)
		}
	case *ast.IndexExpr:
		collectExprRootIdents(e.Object, out)
		collectExprRootIdents(e.Index, out)
		collectExprRootIdents(e.Index2, out)
		collectExprRootIdents(e.Fallback, out)
	case *ast.CastExpr:
		// A postfix value cast/ctor (`box.data[i].char()`) is a CastExpr, not a method
		// call — the driven resource is rooted in its operand (`box`), so descend.
		collectExprRootIdents(e.Operand, out)
	case *ast.BinaryExpr:
		collectExprRootIdents(e.Left, out)
		collectExprRootIdents(e.Right, out)
	case *ast.UnaryExpr:
		collectExprRootIdents(e.Operand, out)
	case *ast.ParenExpr:
		collectExprRootIdents(e.Inner, out)
	case *ast.SliceExpr:
		collectExprRootIdents(e.Object, out)
		collectExprRootIdents(e.Start, out)
		collectExprRootIdents(e.End, out)
	case *ast.ListLitExpr:
		for _, elem := range e.Elems {
			collectExprRootIdents(elem, out)
		}
		for _, key := range e.Keys {
			collectExprRootIdents(key, out)
		}
		collectExprRootIdents(e.Owner, out)
	case *ast.MembershipRangeExpr:
		collectExprRootIdents(e.Start, out)
		collectExprRootIdents(e.End, out)
	case *ast.ListComprehensionExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Source, out)
		collectExprRootIdents(e.RangeEnd, out)
		collectExprRootIdents(e.RangeStep, out)
		collectExprRootIdents(e.Filter, out)
		collectExprRootIdents(e.Owner, out)
		collectExprRootIdents(e.Key, out)
		collectExprRootIdents(e.LoweredParallel, out)
		for _, binding := range e.Bindings {
			collectStmtRootIdents(binding, out)
		}
	case *ast.QueryExpr:
		collectExprRootIdents(e.Source, out)
		collectExprRootIdents(e.Filter, out)
		collectExprRootIdents(e.Projection, out)
		collectExprRootIdents(e.Owner, out)
	case *ast.TernaryExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Cond, out)
		collectExprRootIdents(e.Alt, out)
	case *ast.AddrOfExpr:
		collectExprRootIdents(e.Operand, out)
	case *ast.SpecializeExpr:
		collectExprRootIdents(e.Operand, out)
	case *ast.StructLitExpr:
		for _, arg := range e.Args {
			collectExprRootIdents(arg, out)
		}
		for _, spread := range e.Spreads {
			collectExprRootIdents(spread, out)
		}
	case *ast.RecordUpdateExpr:
		collectExprRootIdents(e.Base, out)
		for _, arg := range e.Args {
			collectExprRootIdents(arg, out)
		}
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			collectExprRootIdents(elem, out)
		}
	case *ast.OptionalBindExpr:
		collectExprRootIdents(e.Value, out)
	case *ast.AllocExpr:
		collectExprRootIdents(e.Owner, out)
		collectExprRootIdents(e.Value, out)
	case *ast.CanExpr:
		collectExprRootIdents(e.Expr, out)
	case *ast.RaiseExpr:
		collectExprRootIdents(e.Error, out)
	case *ast.IsPatternExpr:
		for _, target := range e.Targets {
			collectExprRootIdents(target, out)
		}
	case *ast.IsAliasExpr:
		collectExprRootIdents(e.Target, out)
	case *ast.ExprBlock:
		for _, stmt := range e.Stmts {
			collectStmtRootIdents(stmt, out)
		}
		collectExprRootIdents(e.Value, out)
	case *ast.LambdaExpr:
		for _, stmt := range e.Body {
			collectStmtRootIdents(stmt, out)
		}
		collectExprRootIdents(e.BodyExpr, out)
	case *ast.FoldExpr:
		collectExprRootIdents(e.Value, out)
		for _, arm := range e.Arms {
			collectExprRootIdents(arm.Guard, out)
			for _, stmt := range arm.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.EmitExpr:
		collectExprRootIdents(e.Value, out)
	case *ast.TryExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			collectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.GetExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			collectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.UnwrapElseExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Fallback, out)
		if e.Recovery != nil {
			collectExprRootIdents(e.Recovery.Value, out)
			for _, stmt := range e.Recovery.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.CatchExpr:
		collectExprRootIdents(e.Value, out)
		for _, stmt := range e.Success.Body {
			collectStmtRootIdents(stmt, out)
		}
		for _, arm := range e.Arms {
			for _, stmt := range arm.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	case *ast.MatchExpr:
		collectExprRootIdents(e.Value, out)
		collectExprRootIdents(e.Store, out)
		for _, arm := range e.Arms {
			collectExprRootIdents(arm.Guard, out)
			for _, stmt := range arm.Body {
				collectStmtRootIdents(stmt, out)
			}
		}
	}
}

// collectStmtRootIdents extends collectExprRootIdents into statement-bearing expression
// children (blocks, lambdas, recovery/handler arms, and nested control flow). Those bodies
// execute conditionally but still need their driven-resource roots licensed by the enclosing
// machine.
func collectStmtRootIdents(stmt ast.Stmt, out map[string]bool) {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		collectExprRootIdents(s.Expr, out)
	case *ast.AssignStmt:
		collectExprRootIdents(s.Target, out)
		collectExprRootIdents(s.Value, out)
	case *ast.AugAssignStmt:
		collectExprRootIdents(s.Target, out)
		collectExprRootIdents(s.Value, out)
	case *ast.AsRefAssignStmt:
		collectExprRootIdents(s.Target, out)
		collectExprRootIdents(s.Value, out)
	case *ast.VarDeclStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.LetDestructureStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.TupleBindStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.MoveBindStmt:
		collectExprRootIdents(s.Value, out)
		collectExprRootIdents(s.Store, out)
	case *ast.DeferStmt:
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.ReturnStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.IfStmt:
		collectExprRootIdents(s.Cond, out)
		for _, nested := range s.Then {
			collectStmtRootIdents(nested, out)
		}
		for _, clause := range s.Elifs {
			collectExprRootIdents(clause.Cond, out)
			for _, nested := range clause.Body {
				collectStmtRootIdents(nested, out)
			}
		}
		for _, nested := range s.Else {
			collectStmtRootIdents(nested, out)
		}
	case *ast.WhileStmt:
		collectExprRootIdents(s.Cond, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.ForStmt:
		collectExprRootIdents(s.Start, out)
		collectExprRootIdents(s.End, out)
		collectExprRootIdents(s.Step, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.IterForStmt:
		collectExprRootIdents(s.Source, out)
		collectExprRootIdents(s.WhereFilter, out)
		collectExprRootIdents(s.Filter, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.ParallelForStmt:
		collectExprRootIdents(s.Source, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.MatchStmt:
		collectExprRootIdents(s.Value, out)
		collectExprRootIdents(s.Store, out)
		for _, arm := range s.Arms {
			collectExprRootIdents(arm.Guard, out)
			for _, nested := range arm.Body {
				collectStmtRootIdents(nested, out)
			}
		}
	case *ast.ExpectPatternStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.InStoreStmt:
		collectExprRootIdents(s.Store, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.CanStmt:
		for _, arg := range s.HandlerArgs {
			collectExprRootIdents(arg, out)
		}
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.ScopeStmt:
		collectExprRootIdents(s.Guard, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.PoolStmt:
		collectExprRootIdents(s.Workers, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.LockStmt:
		collectExprRootIdents(s.Mutex, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.PanicStmt:
		collectExprRootIdents(s.Message, out)
	case *ast.StaticIfStmt:
		collectExprRootIdents(s.Cond, out)
		for _, nested := range s.Then {
			collectStmtRootIdents(nested, out)
		}
		for _, clause := range s.Elifs {
			collectExprRootIdents(clause.Cond, out)
			for _, nested := range clause.Body {
				collectStmtRootIdents(nested, out)
			}
		}
		for _, nested := range s.Else {
			collectStmtRootIdents(nested, out)
		}
	case *ast.StaticErrorStmt:
		collectExprRootIdents(s.Message, out)
	case *ast.StaticAssertStmt:
		collectExprRootIdents(s.Cond, out)
		collectExprRootIdents(s.Message, out)
	case *ast.AssertByStmt:
		collectExprRootIdents(s.Cond, out)
		for _, nested := range s.Proof {
			collectStmtRootIdents(nested, out)
		}
	case *ast.ProofBlockStmt:
		collectExprRootIdents(s.Goal, out)
		for _, nested := range s.Proof {
			collectStmtRootIdents(nested, out)
		}
	case *ast.ProofUseStmt:
		for _, citation := range s.Citations {
			collectExprRootIdents(citation, out)
		}
	case *ast.ContractStmt:
		collectExprRootIdents(s.Cond, out)
		for _, nested := range s.Proof {
			collectStmtRootIdents(nested, out)
		}
		for _, arg := range s.UsesArgs {
			collectExprRootIdents(arg, out)
		}
	case *ast.StaticBlockStmt:
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.DiscardStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.RegionStmt:
		collectExprRootIdents(s.Capacity, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.PromoteStmt:
		collectExprRootIdents(s.Value, out)
	case *ast.CheckpointStmt:
		collectExprRootIdents(s.Target, out)
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	case *ast.GroupedCheckpointStmt:
		for _, target := range s.Targets {
			collectExprRootIdents(target, out)
		}
		for _, nested := range s.Body {
			collectStmtRootIdents(nested, out)
		}
	}
}

// exprMentionsIdent reports whether an expression references the named identifier —
// distinguishing `Expr(depth > 1)` (a condition over the payload) from `Expr(1)` /
// `Expr(limit)` (an equality against the payload's scalar local).
func exprMentionsIdent(expr ast.Expr, name string) bool {
	found := false
	var walk func(ast.Expr)
	var walkStatements func([]ast.Stmt)
	walk = func(e ast.Expr) {
		if found || e == nil {
			return
		}
		switch n := e.(type) {
		case *ast.Ident:
			if n.Name == name {
				found = true
			}
		case *ast.FieldExpr:
			walk(n.Object)
		case *ast.CallExpr:
			walk(n.Func)
			for _, a := range n.Args {
				walk(a)
			}
		case *ast.IndexExpr:
			walk(n.Object)
			walk(n.Index)
			walk(n.Index2)
			walk(n.Fallback)
		case *ast.SliceExpr:
			walk(n.Object)
			walk(n.Start)
			walk(n.End)
		case *ast.CastExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.ParenExpr:
			walk(n.Inner)
		case *ast.ListLitExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
			for _, key := range n.Keys {
				walk(key)
			}
			walk(n.Owner)
		case *ast.MembershipRangeExpr:
			walk(n.Start)
			walk(n.End)
		case *ast.ListComprehensionExpr:
			walk(n.Value)
			walk(n.Source)
			walk(n.RangeEnd)
			walk(n.RangeStep)
			walk(n.Filter)
			walk(n.Owner)
			walk(n.Key)
			walk(n.LoweredParallel)
			walkStatements(n.Bindings)
		case *ast.QueryExpr:
			walk(n.Source)
			walk(n.Filter)
			walk(n.Projection)
			walk(n.Owner)
		case *ast.TernaryExpr:
			walk(n.Value)
			walk(n.Cond)
			walk(n.Alt)
		case *ast.AddrOfExpr:
			walk(n.Operand)
		case *ast.SpecializeExpr:
			walk(n.Operand)
		case *ast.StructLitExpr:
			for _, arg := range n.Args {
				walk(arg)
			}
			for _, spread := range n.Spreads {
				walk(spread)
			}
		case *ast.RecordUpdateExpr:
			walk(n.Base)
			for _, arg := range n.Args {
				walk(arg)
			}
		case *ast.TupleExpr:
			for _, elem := range n.Elems {
				walk(elem)
			}
		case *ast.OptionalBindExpr:
			walk(n.Value)
		case *ast.AllocExpr:
			walk(n.Owner)
			walk(n.Value)
		case *ast.CanExpr:
			walk(n.Expr)
		case *ast.RaiseExpr:
			walk(n.Error)
		case *ast.IsPatternExpr:
			for _, target := range n.Targets {
				walk(target)
			}
		case *ast.IsAliasExpr:
			walk(n.Target)
		case *ast.ExprBlock:
			walkStatements(n.Stmts)
			walk(n.Value)
		case *ast.LambdaExpr:
			walkStatements(n.Body)
			walk(n.BodyExpr)
		case *ast.FoldExpr:
			walk(n.Value)
			for _, arm := range n.Arms {
				walk(arm.Guard)
				walkStatements(arm.Body)
			}
		case *ast.EmitExpr:
			walk(n.Value)
		case *ast.TryExpr:
			walk(n.Value)
			walk(n.Fallback)
			if n.Recovery != nil {
				walk(n.Recovery.Value)
				walkStatements(n.Recovery.Body)
			}
		case *ast.GetExpr:
			walk(n.Value)
			walk(n.Fallback)
			if n.Recovery != nil {
				walk(n.Recovery.Value)
				walkStatements(n.Recovery.Body)
			}
		case *ast.UnwrapElseExpr:
			walk(n.Value)
			walk(n.Fallback)
			if n.Recovery != nil {
				walk(n.Recovery.Value)
				walkStatements(n.Recovery.Body)
			}
		case *ast.CatchExpr:
			walk(n.Value)
			walkStatements(n.Success.Body)
			for _, arm := range n.Arms {
				walkStatements(arm.Body)
			}
		case *ast.MatchExpr:
			walk(n.Value)
			walk(n.Store)
			for _, arm := range n.Arms {
				walk(arm.Guard)
				walkStatements(arm.Body)
			}
		}
	}
	walkStatements = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			if found {
				return
			}
			switch s := stmt.(type) {
			case *ast.ExprStmt:
				walk(s.Expr)
			case *ast.AssignStmt:
				walk(s.Target)
				walk(s.Value)
			case *ast.AugAssignStmt:
				walk(s.Target)
				walk(s.Value)
			case *ast.AsRefAssignStmt:
				walk(s.Target)
				walk(s.Value)
			case *ast.VarDeclStmt:
				walk(s.Value)
			case *ast.LetDestructureStmt:
				walk(s.Value)
			case *ast.TupleBindStmt:
				walk(s.Value)
			case *ast.MoveBindStmt:
				walk(s.Value)
				walk(s.Store)
			case *ast.DeferStmt:
				walkStatements(s.Body)
			case *ast.ReturnStmt:
				walk(s.Value)
			case *ast.IfStmt:
				walk(s.Cond)
				walkStatements(s.Then)
				for _, clause := range s.Elifs {
					walk(clause.Cond)
					walkStatements(clause.Body)
				}
				walkStatements(s.Else)
			case *ast.WhileStmt:
				walk(s.Cond)
				walkStatements(s.Body)
			case *ast.ForStmt:
				walk(s.Start)
				walk(s.End)
				walk(s.Step)
				walkStatements(s.Body)
			case *ast.IterForStmt:
				walk(s.Source)
				walk(s.WhereFilter)
				walk(s.Filter)
				walkStatements(s.Body)
			case *ast.ParallelForStmt:
				walk(s.Source)
				walkStatements(s.Body)
			case *ast.MatchStmt:
				walk(s.Value)
				walk(s.Store)
				for _, arm := range s.Arms {
					walk(arm.Guard)
					walkStatements(arm.Body)
				}
			case *ast.ExpectPatternStmt:
				walk(s.Value)
			case *ast.InStoreStmt:
				walk(s.Store)
				walkStatements(s.Body)
			case *ast.CanStmt:
				for _, arg := range s.HandlerArgs {
					walk(arg)
				}
				walkStatements(s.Body)
			case *ast.ScopeStmt:
				walk(s.Guard)
				walkStatements(s.Body)
			case *ast.PoolStmt:
				walk(s.Workers)
				walkStatements(s.Body)
			case *ast.LockStmt:
				walk(s.Mutex)
				walkStatements(s.Body)
			case *ast.PanicStmt:
				walk(s.Message)
			case *ast.StaticIfStmt:
				walk(s.Cond)
				walkStatements(s.Then)
				for _, clause := range s.Elifs {
					walk(clause.Cond)
					walkStatements(clause.Body)
				}
				walkStatements(s.Else)
			case *ast.StaticErrorStmt:
				walk(s.Message)
			case *ast.StaticAssertStmt:
				walk(s.Cond)
				walk(s.Message)
			case *ast.AssertByStmt:
				walk(s.Cond)
				walkStatements(s.Proof)
			case *ast.ProofBlockStmt:
				walk(s.Goal)
				walkStatements(s.Proof)
			case *ast.ProofUseStmt:
				for _, citation := range s.Citations {
					walk(citation)
				}
			case *ast.ContractStmt:
				walk(s.Cond)
				walkStatements(s.Proof)
				for _, arg := range s.UsesArgs {
					walk(arg)
				}
			case *ast.StaticBlockStmt:
				walkStatements(s.Body)
			case *ast.DiscardStmt:
				walk(s.Value)
			case *ast.RegionStmt:
				walk(s.Capacity)
				walkStatements(s.Body)
			case *ast.PromoteStmt:
				walk(s.Value)
			case *ast.CheckpointStmt:
				walk(s.Target)
				walkStatements(s.Body)
			case *ast.GroupedCheckpointStmt:
				for _, target := range s.Targets {
					walk(target)
				}
				walkStatements(s.Body)
			}
		}
	}
	walk(expr)
	return found
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
