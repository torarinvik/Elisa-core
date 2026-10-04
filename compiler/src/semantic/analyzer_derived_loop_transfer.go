package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// This domain is independent of the ordinary analyzer's effects, borrows,
// optimization caches and diagnostic side effects. Keys are resolved Symbols,
// never names. Nil flow means unreachable; a nonnil empty map is reachable.
type derivedLoopState map[*Symbol]Type

type derivedLoopEdges struct {
	fall, broken, continued derivedLoopState
}

type derivedLoopTransfer struct {
	a      *Analyzer
	fuel   int
	failed bool
}

func (a *Analyzer) captureDerivedLoopEntry(scope *Scope) derivedLoopState {
	out := derivedLoopState{}
	seen := map[string]bool{}
	for cur := scope; cur != nil; cur = cur.Parent {
		for name, sym := range cur.Symbols {
			if seen[name] {
				continue
			}
			seen[name] = true
			base, ok := trackedNamedStateStructBase(sym.Type)
			if !ok || base == nil || base.ProtocolStates || len(base.DerivedStates) == 0 {
				continue
			}
			out[sym] = a.currentTrackedValueType(sym)
		}
	}
	return derivedLoopState(a.cloneTrackedValueTypeMapWithSeen(map[*Symbol]Type(out), map[Type]Type{}))
}

func (t *derivedLoopTransfer) clone(s derivedLoopState) derivedLoopState {
	if s == nil {
		return nil
	}
	// Share one clone memo across bindings so common declaration/type graphs
	// are copied once per frontier, not once per variable. Transfers replace
	// types rather than mutating shared type graphs.
	return derivedLoopState(t.a.cloneTrackedValueTypeMapWithSeen(map[*Symbol]Type(s), map[Type]Type{}))
}

func (t *derivedLoopTransfer) widen(s derivedLoopState) {
	for root, typ := range s {
		if widened, ok := t.a.widenNamedStatesDeep(typ); ok {
			s[root] = widened
		}
	}
}

// The lexical universe excludes body-local bindings at a loop head or join.
// Missing facts on a live predecessor are unknown, not declaration evidence.
func (t *derivedLoopTransfer) join(left, right, universe derivedLoopState) derivedLoopState {
	if left == nil && right == nil {
		return nil
	}
	out := derivedLoopState{}
	for root, declared := range universe {
		l, lok := left[root]
		r, rok := right[root]
		if left == nil {
			l, lok = r, rok
		}
		if right == nil {
			r, rok = l, lok
		}
		if lok && rok {
			if merged, ok := t.a.mergeSpecializedValueTypes(l, r); ok {
				out[root] = merged
				continue
			}
		}
		out[root] = t.a.cloneTrackedValueType(declared)
		if widened, ok := t.a.widenNamedStatesDeep(declared); ok {
			out[root] = widened
		}
	}
	return out
}

func derivedLoopEqual(left, right derivedLoopState) bool {
	if (left == nil) != (right == nil) || len(left) != len(right) {
		return false
	}
	for root, typ := range left {
		peer, ok := right[root]
		if !ok || !SameType(typ, peer) {
			return false
		}
	}
	return true
}

func (t *derivedLoopTransfer) step(depth int) bool {
	if depth > 128 || t.fuel <= 0 {
		t.failed = true
		return false
	}
	t.fuel--
	return true
}

func (t *derivedLoopTransfer) require(expected, actual Type, expr ast.Expr, report bool) {
	if !report || expected == nil || actual == nil || expr == nil || IsInvalidType(expected) || IsInvalidType(actual) {
		return
	}
	// The ordinary pass checks borrow/capability adaptation. This pass checks
	// the state precondition, including implicit borrowing of an owned value.
	if base, ok := trackedNamedStateStructBase(expected); ok && base != nil && !base.ProtocolStates && len(base.DerivedStates) != 0 && !AssignableTo(peelNamedStateRefs(StripAggregateStateType(expected)), peelNamedStateRefs(StripAggregateStateType(actual))) {
		t.a.errorf(expr.Pos(), "later loop iteration expects %s, got %s from current derived state", expected, actual)
	}
}

// Only literal primitive syntax can justify state-independent reclassification.
// In particular an identifier's first-iteration const/SMT fact is not reusable.
func derivedLoopLiteral(expr ast.Expr, depth int) bool {
	if expr == nil || depth > 128 {
		return false
	}
	switch n := expr.(type) {
	case *ast.IntLit, *ast.BoolLit, *ast.CharLit, *ast.FloatLit, *ast.StringLit, *ast.NullLit:
		return true
	case *ast.ParenExpr:
		return derivedLoopLiteral(n.Inner, depth+1)
	case *ast.UnaryExpr:
		return n.LoweredCall == nil && derivedLoopLiteral(n.Operand, depth+1)
	case *ast.BinaryExpr:
		return n.LoweredCall == nil && derivedLoopLiteral(n.Left, depth+1) && derivedLoopLiteral(n.Right, depth+1)
	default:
		return false
	}
}

func (t *derivedLoopTransfer) update(current Type, updates map[string]ast.Expr) Type {
	base, ok := trackedNamedStateStructBase(current)
	if !ok || base == nil || base.ProtocolStates {
		return current
	}
	changed := false
	for _, rule := range base.DerivedStates {
		if !t.step(0) {
			return replaceTrackedNamedStateArg(current, unknownNamedStateType(base))
		}
		if recordUpdatePredicateMayChange(rule.Condition, updates, 0) {
			changed = true
			break
		}
	}
	if !changed && len(base.DerivedStates) != 0 {
		return current
	}
	constants := map[string]ast.Expr{}
	for field, value := range updates {
		if derivedLoopLiteral(value, 0) {
			constants[field] = value
		}
	}
	possible := []string{}
	for _, state := range base.NamedStateCases {
		if !t.step(0) {
			return replaceTrackedNamedStateArg(current, unknownNamedStateType(base))
		}
		known, holds := t.a.evaluateDerivedStateForFields(base, state, constants)
		if !known {
			return replaceTrackedNamedStateArg(current, unknownNamedStateType(base))
		}
		if holds {
			possible = append(possible, state)
		}
	}
	if len(possible) != 1 {
		return replaceTrackedNamedStateArg(current, unknownNamedStateType(base))
	}
	return replaceTrackedNamedStateArg(current, newNamedStateType(base.Name, base.NamedStateCases, possible))
}

func (t *derivedLoopTransfer) expr(expr ast.Expr, state derivedLoopState, report bool, depth int) Type {
	if expr == nil {
		return nil
	}
	if !t.step(depth) {
		t.widen(state)
		return t.a.exprTypes[expr]
	}
	cached := t.a.exprTypes[expr]
	switch n := expr.(type) {
	case *ast.Ident:
		if root := t.a.derivedLoopBindings[n]; root != nil {
			if current, ok := state[root]; ok {
				return current
			}
		}
	case *ast.IntLit, *ast.BoolLit, *ast.CharLit, *ast.FloatLit, *ast.StringLit, *ast.NullLit:
	case *ast.ParenExpr:
		return t.expr(n.Inner, state, report, depth+1)
	case *ast.FieldExpr:
		object := t.expr(n.Object, state, report, depth+1)
		if projected, ok := t.a.lookupResolvedFieldType(object, n.Field); ok {
			return projected
		}
	case *ast.UnaryExpr:
		operand := t.expr(n.Operand, state, report, depth+1)
		if n.LoweredCall != nil {
			t.expr(n.LoweredCall, state, report, depth+1)
		} else if adapted, ok := applyNamedStateFromActualType(cached, operand); ok {
			return adapted
		}
	case *ast.BinaryExpr:
		t.expr(n.Left, state, report, depth+1)
		t.expr(n.Right, state, report, depth+1)
		if n.LoweredCall != nil {
			t.expr(n.LoweredCall, state, report, depth+1)
		}
	case *ast.RecordUpdateExpr:
		// The source is captured before replacement arguments can mutate it.
		source := t.a.cloneTrackedValueType(t.expr(n.Base, state, report, depth+1))
		updates := map[string]ast.Expr{}
		for i, arg := range n.Args {
			t.expr(arg, state, report, depth+1)
			updates[n.ArgName(i)] = arg
		}
		result := t.update(source, updates)
		t.require(cached, result, expr, report)
		return result
	case *ast.CallExpr:
		t.expr(n.Func, state, report, depth+1)
		fn, _ := t.a.exprTypes[n.Func].(*FuncType)
		args := n.LoweredArgs()
		for i, arg := range args {
			actual := t.expr(arg, state, report, depth+1)
			if fn != nil && i < len(fn.Params) {
				t.require(fn.Params[i], actual, arg, report)
			}
		}
		// Replay existing checked unconditional poststates using the pure type
		// transform, not the effectful call analyzer. Unknown outcomes retain its
		// conservative widening. Everything without such a summary stays unknown.
		poststates := derivedLoopState{}
		if fn != nil {
			for i, arg := range args {
				if i >= len(fn.Params) {
					break
				}
				if _, borrowed := fn.Params[i].(*RefType); !borrowed {
					continue
				}
				ident, plain := stripOptimizationParens(arg).(*ast.Ident)
				if !plain {
					continue
				}
				root := t.a.derivedLoopBindings[ident]
				current := state[root]
				rows := funcPoststatesForParam(fn.Poststates, i)
				if root == nil || current == nil {
					continue
				}
				t.require(fn.Params[i], current, arg, report)
				if len(rows) == 0 {
					continue
				}
				if next, changed := t.a.computeCallArgPoststateTrackedType(current, nil, rows, false, false); changed && next != nil {
					if prior, duplicate := poststates[root]; duplicate {
						if merged, ok := t.a.mergeSpecializedValueTypes(prior, next); ok {
							next = merged
						} else {
							next, _ = t.a.widenNamedStatesDeep(current)
						}
					}
					poststates[root] = next
				}
			}
		}
		t.widen(state)
		for root, next := range poststates {
			state[root] = next
		}
	case *ast.StructLitExpr:
		literal := true
		for _, arg := range n.Args {
			t.expr(arg, state, report, depth+1)
			literal = literal && derivedLoopLiteral(arg, 0)
		}
		if !literal {
			if widened, ok := t.a.widenNamedStatesDeep(cached); ok {
				return widened
			}
		}
	case *ast.ExprBlock:
		edges := t.block(n.Stmts, state, report, depth+1)
		if edges.broken != nil || edges.continued != nil {
			t.failed = true
		}
		if edges.fall == nil {
			t.widen(state)
			return cached
		}
		for root := range state {
			state[root] = edges.fall[root]
		}
		return t.expr(n.Value, state, report, depth+1)
	default:
		// Unsupported value forms must not reuse first-iteration evidence.
		t.widen(state)
		t.failed = true
	}
	return cached
}

func (t *derivedLoopTransfer) block(body []ast.Stmt, entry derivedLoopState, report bool, depth int) derivedLoopEdges {
	state := t.clone(entry)
	out := derivedLoopEdges{}
	for _, stmt := range body {
		if state == nil {
			break
		}
		if !t.step(depth) {
			t.widen(state)
			break
		}
		switch n := stmt.(type) {
		case *ast.PassStmt:
		case *ast.VarDeclStmt:
			actual := t.expr(n.Value, state, report, depth+1)
			if root := t.a.derivedLoopLocals[n]; root != nil {
				t.require(root.Type, actual, n.Value, report)
				if actual != nil {
					state[root] = t.a.cloneTrackedValueType(actual)
				} else {
					state[root] = root.Type
					t.widen(state)
				}
			}
		case *ast.AssignStmt:
			// Target evaluation may itself have effects; scalar-reference writes
			// can alias a predicate field through another binding.
			t.expr(n.Target, state, report, depth+1)
			actual := t.expr(n.Value, state, report, depth+1)
			if n.WriteThrough || t.a.derivedLoopAliasWrites[n] {
				t.widen(state)
				break
			}
			switch target := stripOptimizationParens(n.Target).(type) {
			case *ast.Ident:
				if root := t.a.derivedLoopBindings[target]; root != nil {
					t.require(root.Type, actual, n.Value, report)
					state[root] = actual
				}
			case *ast.FieldExpr:
				if ident, ok := stripOptimizationParens(target.Object).(*ast.Ident); ok {
					if root := t.a.derivedLoopBindings[ident]; root != nil {
						current := state[root]
						fieldType, fieldKnown := t.a.lookupResolvedFieldType(current, target.Field)
						if isBorrowLikeType(current) || !fieldKnown || isBorrowLikeType(fieldType) || root.AliasOf != nil || isGlobalStorageSymbol(root) {
							t.widen(state)
						} else {
							// A tracked borrow may point into this owned root. Without
							// a disjointness certificate it cannot retain stale state.
							for alias, typ := range state {
								if alias != root && isBorrowLikeType(typ) {
									if widened, ok := t.a.widenNamedStatesDeep(typ); ok {
										state[alias] = widened
									}
								}
							}
						}
						state[root] = t.update(current, map[string]ast.Expr{target.Field: n.Value})
						break
					}
				}
				t.widen(state)
			default:
				t.widen(state)
			}
		case *ast.ExprStmt:
			t.expr(n.Expr, state, report, depth+1)
		case *ast.ReturnStmt:
			actual := t.expr(n.Value, state, report, depth+1)
			t.require(t.a.currentReturn, actual, n.Value, report)
			state = nil
		case *ast.BreakStmt:
			out.broken = t.join(out.broken, state, entry)
			state = nil
		case *ast.ContinueStmt:
			out.continued = t.join(out.continued, state, entry)
			state = nil
		case *ast.IfStmt:
			t.expr(n.Cond, state, report, depth+1)
			if literal, ok := n.Cond.(*ast.BoolLit); ok && literal.Value {
				edges := t.block(n.Then, state, report, depth+1)
				out.broken = t.join(out.broken, edges.broken, entry)
				out.continued = t.join(out.continued, edges.continued, entry)
				state = edges.fall
				break
			}
			then := derivedLoopEdges{}
			if literal, ok := n.Cond.(*ast.BoolLit); !ok || literal.Value {
				then = t.block(n.Then, state, report, depth+1)
			}
			remaining := t.clone(state)
			otherwise := derivedLoopEdges{}
			// Elif conditions/arms retain all possible incoming predecessors.
			for _, arm := range n.Elifs {
				if remaining == nil {
					break
				}
				t.expr(arm.Cond, remaining, report, depth+1)
				if literal, ok := arm.Cond.(*ast.BoolLit); ok && !literal.Value {
					continue
				}
				edges := t.block(arm.Body, remaining, report, depth+1)
				otherwise.fall = t.join(otherwise.fall, edges.fall, state)
				otherwise.broken = t.join(otherwise.broken, edges.broken, state)
				otherwise.continued = t.join(otherwise.continued, edges.continued, state)
				if literal, ok := arm.Cond.(*ast.BoolLit); ok && literal.Value {
					remaining = nil
				}
			}
			last := t.block(n.Else, remaining, report, depth+1)
			otherwise.fall = t.join(otherwise.fall, last.fall, state)
			otherwise.broken = t.join(otherwise.broken, last.broken, state)
			otherwise.continued = t.join(otherwise.continued, last.continued, state)
			state = t.join(then.fall, otherwise.fall, state)
			out.broken = t.join(out.broken, t.join(then.broken, otherwise.broken, entry), entry)
			out.continued = t.join(out.continued, t.join(then.continued, otherwise.continued, entry), entry)
		case *ast.WhileStmt:
			state = t.loop(n.Body, n.Cond, state, report, depth+1)
		case *ast.ForStmt:
			state = t.forLoop(n, state, report, depth+1, false)
		case *ast.CanStmt:
			edges := t.block(n.Body, state, report, depth+1)
			out.broken = t.join(out.broken, edges.broken, entry)
			out.continued = t.join(out.continued, edges.continued, entry)
			state = edges.fall
		default:
			t.widen(state)
			t.failed = true
		}
	}
	out.fall = state
	return out
}

func (t *derivedLoopTransfer) loop(body []ast.Stmt, cond ast.Expr, entry derivedLoopState, report bool, depth int) derivedLoopState {
	if literal, ok := cond.(*ast.BoolLit); ok && !literal.Value {
		return t.clone(entry)
	}
	head := t.clone(entry)
	for iteration := 0; iteration < 64; iteration++ {
		tested := t.clone(head)
		t.expr(cond, tested, false, depth+1)
		back := t.block(body, tested, false, depth+1)
		next := t.join(entry, t.join(back.fall, back.continued, entry), entry)
		if derivedLoopEqual(head, next) {
			tested = t.clone(head)
			t.expr(cond, tested, report, depth+1)
			edges := t.block(body, tested, report, depth+1)
			if literal, ok := cond.(*ast.BoolLit); ok && literal.Value {
				return t.join(nil, edges.broken, entry)
			}
			return t.join(tested, t.join(edges.fall, t.join(edges.continued, edges.broken, entry), entry), entry)
		}
		head = next
		if t.failed {
			break
		}
	}
	t.failed = true
	t.widen(head)
	t.block(body, head, report, depth+1)
	return head
}

func (t *derivedLoopTransfer) forLoop(stmt *ast.ForStmt, entry derivedLoopState, report bool, depth int, boundsObserved bool) derivedLoopState {
	entry = t.clone(entry)
	if !boundsObserved {
		t.expr(stmt.Start, entry, report, depth+1)
		t.expr(stmt.End, entry, report, depth+1)
		t.expr(stmt.Step, entry, report, depth+1)
	}
	known, empty, singleton := t.a.classifyDerivedLiteralRange(stmt)
	if known {
		if empty {
			return t.clone(entry)
		}
		first := t.block(stmt.Body, entry, report, depth+1)
		back := t.join(first.fall, first.continued, entry)
		if singleton || back == nil {
			return t.join(back, first.broken, entry)
		}
		return t.join(t.loop(stmt.Body, nil, back, report, depth+1), first.broken, entry)
	}
	return t.loop(stmt.Body, nil, entry, report, depth+1)
}

func (a *Analyzer) checkDerivedLoopTransfer(body []ast.Stmt, cond ast.Expr, rangeStmt *ast.ForStmt, entry derivedLoopState, pos lexer.Pos) derivedLoopState {
	if len(entry) == 0 {
		return nil
	}
	t := derivedLoopTransfer{a: a, fuel: 8192}
	var exit derivedLoopState
	if rangeStmt != nil {
		exit = t.forLoop(rangeStmt, entry, true, 0, true)
	} else {
		exit = t.loop(body, cond, entry, true, 0)
	}
	if t.failed {
		a.errorf(pos, "derived typestate loop transfer cannot certify unsupported or exhausted control/effect coverage")
		// Never install a partial fixed point, even during a caller's
		// suppressed-diagnostic inference pass.
		exit = t.clone(entry)
		t.widen(exit)
	}
	return exit
}
