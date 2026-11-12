package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/machineroots"
	"fmt"
)

func (p *Parser) parseMachineBranchBody(arm *machineArm) []ast.Stmt {
	var body []ast.Stmt
	for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
		p.skipNewlines()
		if p.peek() == lexer.TOKEN_DEDENT {
			break
		}
		if p.peek() == lexer.TOKEN_IF {
			branch := p.parseMachineBranch(arm)
			body = append(body, branch)
			if !machineroots.CanFallThrough([]ast.Stmt{branch}, arm.branchTransitions) && p.peek() != lexer.TOKEN_DEDENT {
				p.errorf("machine branch continues after every path has reached a terminal decision")
				for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
					p.advance()
				}
				return body
			}
			continue
		}
		if p.peek() == lexer.TOKEN_ARROW {
			arrow := p.advance()
			target := p.expect(lexer.TOKEN_IDENT).Text
			if p.peek() == lexer.TOKEN_LPAREN {
				p.errorf("branch-local machine transitions currently require payload-less states")
				p.advance()
				for p.peek() != lexer.TOKEN_RPAREN && p.peek() != lexer.TOKEN_EOF {
					p.advance()
				}
				p.match(lexer.TOKEN_RPAREN)
			}
			p.expectNewline()
			if arm.branchTransitions == nil {
				arm.branchTransitions = map[lexer.Pos]string{}
			}
			arm.branchTransitions[arrow.Pos] = target
			body = append(body, &ast.BreakStmt{Position: arrow.Pos})
			if p.peek() != lexer.TOKEN_DEDENT {
				p.errorf("machine branch continues after its `->` decision")
				for p.peek() != lexer.TOKEN_DEDENT && p.peek() != lexer.TOKEN_EOF {
					p.advance()
				}
			}
			return body
		}
		stmt := p.parseStmt()
		if stmt != nil {
			body = append(body, stmt)
		}
	}
	return body
}

// validateMachineArmStmt enforces straight-line restrictions and records un-nested exits.
func (p *Parser) validateMachineArmStmt(stmt ast.Stmt, arm *machineArm) {
	switch s := stmt.(type) {
	case *ast.ReturnStmt:
		arm.exit = machineExitReturn
	case *ast.BreakStmt:
		if _, transitionMarker := arm.branchTransitions[s.Position]; !transitionMarker {
			arm.exit = machineExitBreak
		}
	case *ast.ContinueStmt:
		p.errorf("machine arms cannot `continue` — every arm ends in `-> State`, `return`, or `break` (docs/123 §5)")
	case *ast.IfStmt:
		for _, inner := range s.Then {
			p.validateMachineBranchStmt(inner, arm)
		}
		for _, inner := range s.Else {
			p.validateMachineBranchStmt(inner, arm)
		}
		for _, clause := range s.Elifs {
			for _, inner := range clause.Body {
				p.validateMachineBranchStmt(inner, arm)
			}
		}
	case *ast.MatchStmt, *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt:
		p.errorf("machine arms cannot branch or loop (docs/123 §5) — move the condition into the arm header (`State, input if guard:`) or split into separate arms")
	case *ast.CanStmt:
		// A postfix `can Effect` clause wraps its statement in a CanStmt — a transparent
		// effect-licensing wrapper, not a branch. Validate the licensed statements.
		for _, inner := range s.Body {
			p.validateMachineArmStmt(inner, arm)
		}
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.AugAssignStmt, *ast.AsRefAssignStmt, *ast.ExprStmt, *ast.DiscardStmt:
		_ = s // allowed straight-line forms; mutation targets are checked at desugar time
	default:
		p.errorf("machine arms allow only straight-line statements ending in `-> State`, `return`, or `break` (docs/123 §5)")
	}
}

// validateMachineBranchStmt applies the arm's straight-line restrictions inside a branch
// without treating a branch-local return/break as the whole arm's exit.
func (p *Parser) validateMachineBranchStmt(stmt ast.Stmt, arm *machineArm) {
	switch s := stmt.(type) {
	case *ast.ReturnStmt, *ast.BreakStmt:
		return
	case *ast.ContinueStmt:
		p.errorf("machine arms cannot `continue` — every path must end in a transition, return, or break")
	case *ast.IfStmt:
		for _, inner := range s.Then {
			p.validateMachineBranchStmt(inner, arm)
		}
		for _, inner := range s.Else {
			p.validateMachineBranchStmt(inner, arm)
		}
		for _, clause := range s.Elifs {
			for _, inner := range clause.Body {
				p.validateMachineBranchStmt(inner, arm)
			}
		}
	case *ast.MatchStmt, *ast.WhileStmt, *ast.ForStmt, *ast.IterForStmt:
		p.errorf("machine branch cannot contain match or loop control")
	case *ast.CanStmt:
		for _, inner := range s.Body {
			p.validateMachineBranchStmt(inner, arm)
		}
	case *ast.VarDeclStmt, *ast.AssignStmt, *ast.AugAssignStmt, *ast.AsRefAssignStmt, *ast.ExprStmt, *ast.DiscardStmt:
		_ = s
	default:
		p.errorf("machine branches allow straight-line statements and terminal decisions only")
	}
}

// --- desugar -------------------------------------------------------------------------

func (p *Parser) desugarMachine(pos lexer.Pos, suffix string, input, cond, yield ast.Expr, forHeader *machineForHeader, states []machineState, startState string, startArgs []ast.Expr, startSeen bool, arms []machineArm) ast.Stmt {
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
		if machineroots.CanFallThrough(arm.body, arm.branchTransitions) && arm.exit != machineExitTransition {
			p.errorf("machine arm %q makes no decision — end it with `-> State`, `return`, or `break` (docs/123 §5)", arm.state)
		}
		for _, targetName := range arm.branchTransitions {
			target, ok := stateByName[targetName]
			if !ok {
				p.errorf("machine branch transition `-> %s` names undeclared state", targetName)
			} else if len(target.fields) != 0 {
				p.errorf("branch-local machine transitions currently require payload-less states")
			}
		}
		if len(arm.branchTransitions) != 0 && arm.exit == machineExitTransition && len(arm.args) != 0 {
			p.errorf("a shared transition after branch-local arrows cannot carry payload arguments yet")
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
	for i := range arms {
		arms[i].nameSuffix = fmt.Sprintf("%s_a%d", suffix, i)
	}
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
