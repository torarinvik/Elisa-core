package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"fmt"
	"strings"
)

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
		case *ast.IfStmt:
			// Arms may branch (validateMachineArmStmt), so rule 3 must see every branch: an
			// assignment nested in an `if` was never checked and mutated foreign state.
			p.checkMachineArmMutations(s.Then, armLocals, fieldByName, overRoots, mutatedRoots)
			for _, clause := range s.Elifs {
				p.checkMachineArmMutations(clause.Body, armLocals, fieldByName, overRoots, mutatedRoots)
			}
			p.checkMachineArmMutations(s.Else, armLocals, fieldByName, overRoots, mutatedRoots)
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
	var sharedTransition []ast.Stmt
	if arm.exit == machineExitTransition {
		if target, ok := stateByName[arm.target]; ok {
			// The dispatch arm already has a scope of its own, but that is also the
			// scope in which arm-local declarations live.  If an arm-local shadows a
			// target payload field, a name-only backend lookup would otherwise turn
			// `-> Next(value)` into a store to the arm-local.  Put the pre-transition
			// body in one more lexical scope and emit the actual payload stores after
			// it.  Transition arguments are captured before leaving that scope so a
			// valid `-> Next(local)` keeps working as well.
			var transition []ast.Stmt
			if machineArmDeclaresAnyField(body, target.fields) {
				tempDecls, captures, payloadTransition := lowerMachineTransitionAfterScope(arm, target, enumMember, modeVar)
				captures = append(body, captures...)
				body = append(tempDecls, &ast.CanStmt{Position: arm.pos, Body: captures})
				transition = payloadTransition
			} else {
				transition = lowerMachineTransition(arm, target, enumMember, modeVar)
			}
			if len(arm.branchTransitions) != 0 {
				sharedTransition = transition
			} else {
				body = append(body, transition...)
			}
		}
	}
	if len(arm.branchTransitions) != 0 {
		body = lowerMachineBranchTransitions(body, arm.branchTransitions, stateByName, enumMember, modeVar, sharedTransition)
	}
	if len(body) == 0 {
		body = append(body, &ast.PassStmt{Position: arm.pos})
	}
	return loweredMachineArm{pos: arm.pos, cond: cond, body: body}
}

// lowerMachineBranchTransitions replaces the parser's BreakStmt markers with the same
// payload-less mode assignment used by an ordinary arm transition. The nested IfStmt tree
// remains intact, so branch-local decisions preserve source control flow.
func lowerMachineBranchTransitions(stmts []ast.Stmt, targets map[lexer.Pos]string, states map[string]*machineState, enumMember func(lexer.Pos, string) ast.Expr, modeVar string, suffix []ast.Stmt) []ast.Stmt {
	var hasControl func(ast.Stmt) bool
	hasControl = func(stmt ast.Stmt) bool {
		switch n := stmt.(type) {
		case *ast.BreakStmt:
			if _, ok := targets[n.Position]; ok {
				return true
			}
			return true // a source break terminates this path as well
		case *ast.ReturnStmt:
			return true
		case *ast.IfStmt:
			for _, child := range n.Then {
				if hasControl(child) {
					return true
				}
			}
			for _, child := range n.Else {
				if hasControl(child) {
					return true
				}
			}
			for _, clause := range n.Elifs {
				for _, child := range clause.Body {
					if hasControl(child) {
						return true
					}
				}
			}
		}
		return false
	}
	var lowerSequence func([]ast.Stmt, []ast.Stmt) []ast.Stmt
	lowerSequence = func(source, continuation []ast.Stmt) []ast.Stmt {
		if len(source) == 0 {
			return append([]ast.Stmt(nil), continuation...)
		}
		stmt := source[0]
		rest := source[1:]
		switch n := stmt.(type) {
		case *ast.BreakStmt:
			if targetName, ok := targets[n.Position]; ok {
				if target, exists := states[targetName]; exists && len(target.fields) == 0 {
					return []ast.Stmt{&ast.AssignStmt{Position: n.Position, Target: &ast.Ident{Position: n.Position, Name: modeVar}, Value: enumMember(n.Position, targetName)}}
				}
			}
			return []ast.Stmt{stmt}
		case *ast.ReturnStmt:
			return []ast.Stmt{stmt}
		case *ast.IfStmt:
			if !hasControl(stmt) {
				copy := *n
				copy.Then = lowerSequence(n.Then, nil)
				copy.Else = lowerSequence(n.Else, nil)
				copy.Elifs = append([]ast.ElifClause(nil), n.Elifs...)
				for i := range copy.Elifs {
					copy.Elifs[i].Body = lowerSequence(n.Elifs[i].Body, nil)
				}
				return append([]ast.Stmt{&copy}, lowerSequence(rest, continuation)...)
			}
			remaining := lowerSequence(rest, continuation)
			copy := *n
			copy.Then = lowerSequence(n.Then, remaining)
			if len(n.Else) == 0 {
				copy.Else = append([]ast.Stmt(nil), remaining...)
			} else {
				copy.Else = lowerSequence(n.Else, remaining)
			}
			copy.Elifs = append([]ast.ElifClause(nil), n.Elifs...)
			for i := range copy.Elifs {
				copy.Elifs[i].Body = lowerSequence(n.Elifs[i].Body, remaining)
			}
			return []ast.Stmt{&copy}
		default:
			return append([]ast.Stmt{stmt}, lowerSequence(rest, continuation)...)
		}
	}
	return lowerSequence(stmts, suffix)
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
			tmp := fmt.Sprintf("__machine_arg_%s_%d", arm.nameSuffix, i)
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
		name := fmt.Sprintf("__machine_arg_%s_%d", arm.nameSuffix, i)
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
