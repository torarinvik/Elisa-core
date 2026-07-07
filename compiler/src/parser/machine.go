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
	pos       lexer.Pos
	state     string
	payload   []machinePayloadPat
	inputs    []ast.Expr // literal alternatives (`'a' | 'b'`); nil when inputWild
	inputWild bool
	inputBind string // input bind pattern: names the input value for guard/body
	guard     ast.Expr
	body      []ast.Stmt // statements before the exit; excludes the transition line
	exit      machineExitKind
	target    string     // transition target state
	targetPos lexer.Pos
	args      []ast.Expr // transition payload args
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
			states = append(states, p.parseMachineStateDecl())
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

	return p.desugarMachine(pos, input, cond, yield, states, startState, startArgs, startSeen, arms)
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
		arm.inputs = append(arm.inputs, p.parseMatchValuePatternExpr())
		for p.match(lexer.TOKEN_PIPE) {
			arm.inputs = append(arm.inputs, p.parseMatchValuePatternExpr())
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
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.ExprStmt:
		_ = s // allowed straight-line forms; mutation targets are checked at desugar time
	default:
		p.errorf("machine arms allow only straight-line statements ending in `-> State`, `return`, or `break` (docs/123 §5)")
	}
}

// --- desugar -------------------------------------------------------------------------

func (p *Parser) desugarMachine(pos lexer.Pos, input, cond, yield ast.Expr, states []machineState, startState string, startArgs []ast.Expr, startSeen bool, arms []machineArm) ast.Stmt {
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
	for _, st := range states {
		idxs := armsByState[st.name]
		if len(idxs) == 0 {
			p.errorf("machine state %q has no arms — every state must handle every input (docs/123 §5)", st.name)
			continue
		}
		for j, idx := range idxs {
			arm := &arms[idx]
			irrefutable := machineArmIrrefutable(arm)
			if j == len(idxs)-1 {
				if !irrefutable {
					p.errorf("machine state %q does not cover all inputs — its final arm must be the unguarded wildcard `%s, _:` (docs/123 §5)", st.name, st.name)
				}
			} else if irrefutable {
				p.errorf("machine arm %d for state %q is unreachable — an earlier arm already matches every input (docs/123 §5)", j+2, st.name)
				break
			}
		}
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
	for i := range arms {
		armLocals := map[string]bool{}
		for _, arg := range arms[i].args {
			collectMachineCallArgRoots(&ast.ExprStmt{Expr: arg}, overRoots, mutatedRoots)
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
		// Input BIND names are hoisted to the head of the state's match arm — a bound
		// input is referenced from arm GUARDS (if-ladder conditions), which run before
		// any per-arm body statement could declare it.
		var bindDecls []ast.Stmt
		bindSeen := map[string]bool{}
		for _, idx := range armsByState[st.name] {
			arm := &arms[idx]
			if arm.inputBind != "" && !bindSeen[arm.inputBind] {
				bindSeen[arm.inputBind] = true
				bindDecls = append(bindDecls, &ast.VarDeclStmt{Position: arm.pos, Name: arm.inputBind, Value: &ast.Ident{Position: arm.pos, Name: inputVar}})
			}
			loweredArms = append(loweredArms, p.lowerMachineArm(arm, st, stateByName, enumMember, inputVar, modeVar))
		}
		matchArms = append(matchArms, ast.MatchArm{
			Position: st.pos,
			Pattern:  &ast.MatchVariantPattern{Position: st.pos, EnumName: enumName, Variant: st.name},
			Body:     append(bindDecls, buildMachineArmChain(st.pos, loweredArms)...),
		})
	}

	loopCond := cond
	if loopCond == nil {
		loopCond = &ast.BoolLit{Position: pos, Value: true}
	}
	loop := &ast.WhileStmt{
		Position: pos,
		Cond:     loopCond,
		Body: []ast.Stmt{
			&ast.VarDeclStmt{Position: pos, Name: inputVar, Value: input},
			&ast.MatchStmt{Position: pos, Value: &ast.Ident{Position: pos, Name: modeVar}, Arms: matchArms},
		},
	}

	hdr := &loopHeader{pos: pos, decls: decls, captures: sortedKeys(mutatedRoots), yield: yield}
	return p.wrapLoopHeader(hdr, loop)
}

// checkMachineArmMutations enforces the foreign-mutation refusal over an arm body
// (recursing through transparent CanStmt wrappers) and records driven roots that need
// capture licensing. A driven root passed to any call may be mutated THROUGH the call
// (`advance(scanner, 1)` with a `mutable&` param), so those join the capture set too.
func (p *Parser) checkMachineArmMutations(stmts []ast.Stmt, armLocals map[string]bool, fieldByName map[string]machineField, overRoots, mutatedRoots map[string]bool) {
	for _, stmt := range stmts {
		collectMachineCallArgRoots(stmt, overRoots, mutatedRoots)
		switch s := stmt.(type) {
		case *ast.CanStmt:
			p.checkMachineArmMutations(s.Body, armLocals, fieldByName, overRoots, mutatedRoots)
		case *ast.VarDeclStmt:
			armLocals[s.Name] = true
		case *ast.AssignStmt:
			root := assignRootIdent(s.Target)
			if root == "" {
				p.errorf("machine arm assignment target is not rooted in a named binding")
				continue
			}
			if armLocals[root] {
				continue
			}
			if _, isField := fieldByName[root]; isField {
				p.errorf("machine arm assigns payload field %q directly — payloads change only through `-> State(args)` transitions (docs/123 §5)", root)
				continue
			}
			if !overRoots[root] {
				p.errorf("machine arms may only mutate the driven resource (%s) — %q is foreign state (docs/123 §5)", strings.Join(sortedKeys(overRoots), ", "), root)
				continue
			}
			mutatedRoots[root] = true
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
		for _, alt := range arm.inputs {
			eq := &ast.BinaryExpr{Position: arm.pos, Op: lexer.TOKEN_EQEQ, Left: &ast.Ident{Position: arm.pos, Name: inputVar}, Right: alt}
			if inputCond == nil {
				inputCond = eq
			} else {
				inputCond = &ast.BinaryExpr{Position: arm.pos, Op: lexer.TOKEN_OR, Left: inputCond, Right: eq}
			}
		}
		cond = andJoin(cond, inputCond)
	}
	cond = andJoin(cond, arm.guard)

	body := append([]ast.Stmt(nil), aliasDecls...)
	body = append(body, arm.body...)
	if arm.exit == machineExitTransition {
		if target, ok := stateByName[arm.target]; ok {
			body = append(body, lowerMachineTransition(arm, target, enumMember, modeVar)...)
		}
	}
	if len(body) == 0 {
		body = append(body, &ast.PassStmt{Position: arm.pos})
	}
	return loweredMachineArm{pos: arm.pos, cond: cond, body: body}
}

// lowerMachineTransition emits the payload assigns + mode rebind for `-> State(args)`.
// With 2+ args, argument values are captured into temps BEFORE any field assign so
// cross-referencing payloads (`-> Swap(b, a)`) read pre-transition values. No-op assigns
// (`field <- field`, self-state mode rebinds) are elided to keep the lowering the exact
// hand-written pilot shape.
func lowerMachineTransition(arm *machineArm, target *machineState, enumMember func(lexer.Pos, string) ast.Expr, modeVar string) []ast.Stmt {
	var out []ast.Stmt
	values := arm.args
	if len(arm.args) > 1 {
		values = make([]ast.Expr, len(arm.args))
		for i, a := range arm.args {
			if id, ok := a.(*ast.Ident); ok && i < len(target.fields) && id.Name == target.fields[i].name {
				values[i] = a // no-op assign, elided below — no temp needed
				continue
			}
			tmp := fmt.Sprintf("__machine_arg_%d_%d", arm.targetPos.Offset, i)
			out = append(out, &ast.VarDeclStmt{Position: arm.targetPos, Name: tmp, Value: a})
			values[i] = &ast.Ident{Position: arm.targetPos, Name: tmp}
		}
	}
	for i, f := range target.fields {
		if i >= len(values) {
			break
		}
		if id, ok := values[i].(*ast.Ident); ok && id.Name == f.name {
			continue // `depth <- depth` self-assign elided
		}
		out = append(out, &ast.AssignStmt{Position: arm.targetPos, Target: &ast.Ident{Position: arm.targetPos, Name: f.name}, Value: values[i]})
	}
	if target.name != arm.state {
		out = append(out, &ast.AssignStmt{Position: arm.targetPos, Target: &ast.Ident{Position: arm.targetPos, Name: modeVar}, Value: enumMember(arm.targetPos, target.name)})
	}
	return out
}

// buildMachineArmChain folds a state's lowered arms into a single if/elif/else ladder —
// or the bare body when the state has only its irrefutable arm.
func buildMachineArmChain(pos lexer.Pos, arms []loweredMachineArm) []ast.Stmt {
	if len(arms) == 1 && arms[0].cond == nil {
		return arms[0].body
	}
	ifStmt := &ast.IfStmt{Position: arms[0].pos, Cond: arms[0].cond, Then: arms[0].body}
	for _, a := range arms[1:] {
		if a.cond == nil {
			ifStmt.Else = a.body
			break
		}
		ifStmt.Elifs = append(ifStmt.Elifs, ast.ElifClause{Position: a.pos, Cond: a.cond, Body: a.body})
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
		case *ast.CastExpr:
			visit(n.Operand)
		}
	}
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		visit(s.Expr)
	case *ast.AssignStmt:
		visit(s.Value)
	case *ast.VarDeclStmt:
		visit(s.Value)
	case *ast.ReturnStmt:
		visit(s.Value)
	}
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
	}
}

// exprMentionsIdent reports whether an expression references the named identifier —
// distinguishing `Expr(depth > 1)` (a condition over the payload) from `Expr(1)` /
// `Expr(limit)` (an equality against the payload's scalar local).
func exprMentionsIdent(expr ast.Expr, name string) bool {
	found := false
	var walk func(ast.Expr)
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
		case *ast.CastExpr:
			walk(n.Operand)
		case *ast.BinaryExpr:
			walk(n.Left)
			walk(n.Right)
		case *ast.UnaryExpr:
			walk(n.Operand)
		case *ast.ParenExpr:
			walk(n.Inner)
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
