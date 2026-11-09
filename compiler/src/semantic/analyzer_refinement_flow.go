package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// unknown (open). It is the flow-prover's abstraction of what a branch condition tells us about an
// immutable integer variable (docs/85 1d-2).
type numRange struct {
	loKnown bool
	lo      int64
	hiKnown bool
	hi      int64
}

func cloneNumRangeMap(in map[string]numRange) map[string]numRange {
	if in == nil {
		return nil
	}
	out := make(map[string]numRange, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneNumRangeMapByIndex(in map[int]numRange) map[int]numRange {
	if in == nil {
		return nil
	}
	out := make(map[int]numRange, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneScopeRangeFacts(scope *Scope) map[*Scope]map[string]numRange {
	out := map[*Scope]map[string]numRange{}
	for sc := scope; sc != nil; sc = sc.Parent {
		out[sc] = cloneNumRangeMap(sc.rangeFacts)
	}
	return out
}

func restoreScopeRangeFacts(saved map[*Scope]map[string]numRange) {
	for sc, facts := range saved {
		sc.rangeFacts = cloneNumRangeMap(facts)
	}
}

// join widens a range to cover both itself and another (the union of two branch results): a bound is
// kept only if BOTH ranges bound that side, taking the looser endpoint. Used to combine the per-branch
// result ranges of a conditional into the range guaranteed regardless of which branch is taken.
func (r numRange) join(o numRange) numRange {
	var out numRange
	if r.loKnown && o.loKnown {
		out.loKnown = true
		if o.lo < r.lo {
			out.lo = o.lo
		} else {
			out.lo = r.lo
		}
	}
	if r.hiKnown && o.hiKnown {
		out.hiKnown = true
		if o.hi > r.hi {
			out.hi = o.hi
		} else {
			out.hi = r.hi
		}
	}
	return out
}

// intersect tightens a range with another fact about the same variable (conjunction of conditions).
func (r numRange) intersect(o numRange) numRange {
	out := r
	if o.loKnown && (!out.loKnown || o.lo > out.lo) {
		out.loKnown, out.lo = true, o.lo
	}
	if o.hiKnown && (!out.hiKnown || o.hi < out.hi) {
		out.hiKnown, out.hi = true, o.hi
	}
	return out
}

// gatherNumericRangeRefinement records integer-bound facts for an IMMUTABLE identifier or immutable
// struct field path compared against a compile-time-constant in a (truthy) branch condition:
// `a > 5`, `a >= 0`, `s.n <= 103`, `5 < a`, `a == k`, etc. Immutable-only so the fact holds for
// the whole branch with no invalidation. Field-path keys ("s.n") are invalidated by
// invalidateRangeFactsForTarget on any write to s.n or s. Called from
// applyConditionRefinementsInternal for comparison operators.
func (a *Analyzer) gatherNumericRangeRefinement(scope *Scope, n *ast.BinaryExpr, truthy bool) {
	if scope == nil || n == nil {
		return
	}
	// Normalize to `subject OP const`. If the constant is on the left (`5 < a`), flip the operator.
	// Try bare identifier first; fall back to a one-level field-path subject ("s.n").
	op := n.Op
	name, ok := immutableIntIdentName(a, scope, n.Left)
	identExpr := n.Left
	var c int64
	var cok bool
	if ok {
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = immutableIntIdentName(a, scope, n.Right); ok {
		identExpr = n.Right
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	} else if name, ok = fieldPathKey(a, scope, n.Left); ok {
		// `s.n OP const`
		c, cok = a.constIntValue(n.Right)
	} else if name, ok = fieldPathKey(a, scope, n.Right); ok {
		// `const OP s.n` — flip so subject is on the left
		identExpr = n.Right
		c, cok = a.constIntValue(n.Left)
		op = flipComparison(op)
	}
	if !ok || !cok {
		return
	}
	// numRange has signed int64 endpoints. The const folder represents a high
	// u64 literal as negative bits, which cannot describe an unsigned bound.
	// Leave that comparison to the width-aware SMT guard instead.
	if signed, _, typed := smtIntWidthSign(a.exprType(identExpr)); typed && !signed && c < 0 {
		return
	}
	// The FALSY branch (the fall-through after `if a < c: return …`, or an else) narrows by the
	// LOGICAL NEGATION of the comparison: `not (a < c)` is `a >= c`. `==` falsy is `!=`, which is not
	// a single contiguous range, so it contributes nothing (default below). This is what makes an
	// early-return guard establish the post-guard bound for the static provers (docs/85 gap #3).
	if !truthy {
		op = negateComparison(op)
	}
	var fact numRange
	switch op {
	case lexer.TOKEN_GT: // a > c  ⇒ a >= c+1
		fact = numRange{loKnown: true, lo: c + 1}
	case lexer.TOKEN_GTEQ: // a >= c
		fact = numRange{loKnown: true, lo: c}
	case lexer.TOKEN_LT: // a < c  ⇒ a <= c-1
		fact = numRange{hiKnown: true, hi: c - 1}
	case lexer.TOKEN_LTEQ: // a <= c
		fact = numRange{hiKnown: true, hi: c}
	case lexer.TOKEN_EQEQ: // a == c
		fact = numRange{loKnown: true, lo: c, hiKnown: true, hi: c}
	case lexer.TOKEN_BANGEQ: // a != c — a single range only for an UNSIGNED zero-check: a != 0 ⇒ a >= 1
		if signed, _, ok := smtIntWidthSign(a.exprType(identExpr)); ok && !signed && c == 0 {
			fact = numRange{loKnown: true, lo: 1}
		} else {
			return
		}
	default:
		return
	}
	if scope.rangeFacts == nil {
		scope.rangeFacts = map[string]numRange{}
	}
	scope.rangeFacts[name] = scope.rangeFacts[name].intersect(fact)
}

func (a *Analyzer) applyCountUpWhileExitFacts(stmt *ast.WhileStmt) {
	if a == nil || a.currentScope == nil || stmt == nil {
		return
	}
	name, bound, ok := a.countUpLoopUpperBound(stmt.Cond)
	if !ok || !bodyHasOnlyUnitIncrement(stmt.Body, name) {
		return
	}
	if a.currentScope.rangeFacts == nil {
		a.currentScope.rangeFacts = map[string]numRange{}
	}
	a.currentScope.rangeFacts[name] = a.currentScope.rangeFacts[name].intersect(numRange{hiKnown: true, hi: bound})
}

// countUpExitFactSound reports whether `applyCountUpWhileExitFacts` may soundly record `i <= bound`
// after `while i < bound: i++`. The exit fact is true ONLY when `i <= bound` at ENTRY: a count that
// starts at or below bound reaches exactly bound (the +1 step never overshoots), but a count that
// starts ABOVE bound never runs and keeps its too-large value. MUST be evaluated on the pristine
// pre-loop scope (before the body's own `i <- i + 1` mutates the tracked entry value).
func (a *Analyzer) countUpExitFactSound(stmt *ast.WhileStmt) bool {
	if a == nil || a.currentScope == nil || stmt == nil {
		return false
	}
	name, bound, ok := a.countUpLoopUpperBound(stmt.Cond)
	if !ok || !bodyHasOnlyUnitIncrement(stmt.Body, name) {
		return false
	}
	if r, found := a.lookupRangeFact(name); found && r.hiKnown && r.hi <= bound {
		return true
	}
	if c, known := a.lookupWrittenConst(name); known {
		if v, ok := a.constIntValue(c); ok && v <= bound {
			return true
		}
	}
	// A LIVE entry `requires name <= K` (or `< K`) with K <= bound also makes the exit fact sound: the
	// counter starts within the bound and only counts up to it. "Live" means the seeded requires
	// assert-fact still exists in scope — if `name` was reassigned before the loop, the standard mutation
	// invalidation already dropped it, so a pre-loop reassignment correctly disqualifies the bound. This
	// lets `requires i <= 5; while i < 5: i++` prove `result <= 5` SOUNDLY, without re-asserting the entry
	// precondition for the mutated post-loop value (the requires-as-permanent-axiom hole, audit cluster B).
	if hi, ok := a.liveRequiresUpperBound(name); ok && hi <= bound {
		return true
	}
	return false
}

// liveRequiresUpperBound returns the tightest constant K such that a `requires name <= K` (or `< K+1`)
// clause of the enclosing function is STILL a live assert-fact (not dropped by a mutation of name).
func (a *Analyzer) liveRequiresUpperBound(name string) (int64, bool) {
	if a == nil || a.currentFuncDecl == nil || name == "" {
		return 0, false
	}
	best, found := int64(0), false
	for _, req := range a.currentFuncDecl.Requires {
		hi, ok := a.requiresClauseUpperBound(req, name)
		if !ok || !a.assertFactLive(req) {
			continue
		}
		if !found || hi < best {
			best, found = hi, true
		}
	}
	return best, found
}

// assertFactLive reports whether `expr` is still present as a flow assert-fact in the active scope
// chain (seeded and not since invalidated by a mutation of one of its roots).
func (a *Analyzer) assertFactLive(expr ast.Expr) bool {
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if fact.Expr == expr {
				return true
			}
		}
	}
	return false
}

// assertFactSpellingLive reports a live flow assert-fact structurally equal to expr (source
// positions aside). Facts are recorded conjunct by conjunct, so each conjunct of expr must match.
func (a *Analyzer) assertFactSpellingLive(expr ast.Expr) bool {
	if bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr); ok && bin != nil && bin.Op == lexer.TOKEN_AND && bin.LoweredCall == nil {
		return a.assertFactSpellingLive(bin.Left) && a.assertFactSpellingLive(bin.Right)
	}
	for sc := a.currentScope; sc != nil; sc = sc.Parent {
		for _, fact := range sc.smtAssertFacts {
			if astEqualIgnoringPositions(reflect.ValueOf(fact.Expr), reflect.ValueOf(expr)) {
				return true
			}
		}
	}
	return false
}

var lexerPosType = reflect.TypeOf(lexer.Pos{})

// rangeQuantifierShape is `forall/exists name in lo..<hi: predicate`, whether written as a
// quantifier (the guard desugared into the body) or lowered to a runtime flag-and-loop block.
type rangeQuantifierShape struct {
	exists            bool
	name              string
	lo, hi, predicate ast.Expr
}

func (q rangeQuantifierShape) equal(o rangeQuantifierShape) bool {
	return q.exists == o.exists && q.name == o.name &&
		astEqualIgnoringPositions(reflect.ValueOf(q.lo), reflect.ValueOf(o.lo)) &&
		astEqualIgnoringPositions(reflect.ValueOf(q.hi), reflect.ValueOf(o.hi)) &&
		astEqualIgnoringPositions(reflect.ValueOf(q.predicate), reflect.ValueOf(o.predicate))
}

func rangeQuantifierShapeOf(node any) (rangeQuantifierShape, bool) {
	switch n := node.(type) {
	case *ast.ExprBlock:
		loop, predicate, exists, ok := runtimeRangeQuantifierParts(n)
		if !ok {
			return rangeQuantifierShape{}, false
		}
		return rangeQuantifierShape{exists: exists, name: loop.Name, lo: loop.Start, hi: loop.End, predicate: predicate}, true
	case *ast.QuantifierExpr:
		if n.In != nil || len(n.Vars) != 1 {
			return rangeQuantifierShape{}, false
		}
		body, ok := stripOptimizationParens(n.Body).(*ast.BinaryExpr)
		if !ok || body.LoweredCall != nil {
			return rangeQuantifierShape{}, false
		}
		guardExpr, predicate := body.Left, body.Right
		if n.Exists {
			if body.Op != lexer.TOKEN_AND {
				return rangeQuantifierShape{}, false
			}
		} else {
			not, ok := stripOptimizationParens(body.Left).(*ast.UnaryExpr)
			if body.Op != lexer.TOKEN_OR || !ok || not.Op != lexer.TOKEN_NOT || not.LoweredCall != nil {
				return rangeQuantifierShape{}, false
			}
			guardExpr = not.Operand
		}
		guard, ok := stripOptimizationParens(guardExpr).(*ast.BinaryExpr)
		if !ok || guard.Op != lexer.TOKEN_AND {
			return rangeQuantifierShape{}, false
		}
		low, okLow := stripOptimizationParens(guard.Left).(*ast.BinaryExpr)
		high, okHigh := stripOptimizationParens(guard.Right).(*ast.BinaryExpr)
		if !okLow || !okHigh || low.Op != lexer.TOKEN_LTEQ || high.Op != lexer.TOKEN_LT ||
			!isIdentNamed(low.Right, n.Vars[0]) || !isIdentNamed(high.Left, n.Vars[0]) {
			return rangeQuantifierShape{}, false
		}
		return rangeQuantifierShape{exists: n.Exists, name: n.Vars[0], lo: low.Left, hi: high.Right, predicate: predicate}, true
	}
	return rangeQuantifierShape{}, false
}

// astEqualIgnoringPositions compares two AST values field by field, skipping source positions.
func astEqualIgnoringPositions(x, y reflect.Value) bool {
	if x.IsValid() != y.IsValid() {
		return false
	}
	if !x.IsValid() {
		return true
	}
	if x.Type() != y.Type() {
		// A range quantifier and its lowered runtime loop have different node types.
		if x.Kind() == reflect.Pointer && y.Kind() == reflect.Pointer && !x.IsNil() && !y.IsNil() {
			if left, ok := rangeQuantifierShapeOf(x.Interface()); ok {
				if right, ok := rangeQuantifierShapeOf(y.Interface()); ok {
					return left.equal(right)
				}
			}
		}
		return false
	}
	if x.Type() == lexerPosType {
		return true
	}
	if x.Kind() == reflect.Pointer && !x.IsNil() && !y.IsNil() {
		if left, ok := rangeQuantifierShapeOf(x.Interface()); ok {
			right, ok := rangeQuantifierShapeOf(y.Interface())
			return ok && left.equal(right)
		}
	}
	switch x.Kind() {
	case reflect.Pointer, reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		return astEqualIgnoringPositions(x.Elem(), y.Elem())
	case reflect.Struct:
		for i := 0; i < x.NumField(); i++ {
			if !astEqualIgnoringPositions(x.Field(i), y.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if x.Len() != y.Len() {
			return false
		}
		for i := 0; i < x.Len(); i++ {
			if !astEqualIgnoringPositions(x.Index(i), y.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		if x.Len() != y.Len() {
			return false
		}
		for _, key := range x.MapKeys() {
			if !astEqualIgnoringPositions(x.MapIndex(key), y.MapIndex(key)) {
				return false
			}
		}
		return true
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return x.IsNil() && y.IsNil()
	default:
		return x.Equal(y)
	}
}

// requiresClauseUpperBound extracts a constant upper bound on `name` from a `name <= K` / `name < K`
// (or the flipped `K >= name` / `K > name`) clause, or ok=false otherwise.
func (a *Analyzer) requiresClauseUpperBound(expr ast.Expr, name string) (int64, bool) {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil {
		return 0, false
	}
	leftIsName := isIdentNamed(bin.Left, name)
	rightIsName := isIdentNamed(bin.Right, name)
	switch bin.Op {
	case lexer.TOKEN_LTEQ: // name <= K  /  K <= name is a lower bound (ignored)
		if leftIsName {
			if k, ok := a.constIntValue(bin.Right); ok {
				return k, true
			}
		}
	case lexer.TOKEN_LT: // name < K  ⇒  name <= K-1
		if leftIsName {
			if k, ok := a.constIntValue(bin.Right); ok {
				return k - 1, true
			}
		}
	case lexer.TOKEN_GTEQ: // K >= name  ⇒  name <= K
		if rightIsName {
			if k, ok := a.constIntValue(bin.Left); ok {
				return k, true
			}
		}
	case lexer.TOKEN_GT: // K > name  ⇒  name <= K-1
		if rightIsName {
			if k, ok := a.constIntValue(bin.Left); ok {
				return k - 1, true
			}
		}
	}
	return 0, false
}

func isIdentNamed(expr ast.Expr, name string) bool {
	id, ok := stripOptimizationParens(expr).(*ast.Ident)
	return ok && id != nil && id.Name == name
}

func (a *Analyzer) countUpLoopUpperBound(expr ast.Expr) (string, int64, bool) {
	switch n := stripOptimizationParens(expr).(type) {
	case *ast.BinaryExpr:
		if n.Op == lexer.TOKEN_AND {
			if name, bound, ok := a.countUpLoopUpperBound(n.Left); ok {
				return name, bound, true
			}
			return a.countUpLoopUpperBound(n.Right)
		}
		if n.Op != lexer.TOKEN_LT {
			return "", 0, false
		}
		id, ok := stripOptimizationParens(n.Left).(*ast.Ident)
		if !ok || id == nil {
			return "", 0, false
		}
		bound, ok := a.constIntValue(n.Right)
		if !ok {
			return "", 0, false
		}
		return id.Name, bound, true
	default:
		return "", 0, false
	}
}

func bodyHasOnlyUnitIncrement(body []ast.Stmt, name string) bool {
	if name == "" {
		return false
	}
	seenIncrement := false
	for _, stmt := range body {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			if stmtWritesRoot(stmt, name) {
				return false
			}
			continue
		}
		root, ok := rootIdentName(assign.Target)
		if !ok || root != name {
			continue
		}
		if seenIncrement || !isUnitIncrementOf(assign.Value, name) {
			return false
		}
		seenIncrement = true
	}
	return seenIncrement
}

func stmtWritesRoot(stmt ast.Stmt, name string) bool {
	var target ast.Expr
	switch n := stmt.(type) {
	case *ast.AssignStmt:
		target = n.Target
	case *ast.AugAssignStmt:
		target = n.Target
	case *ast.AsRefAssignStmt:
		target = n.Target
	default:
		return false
	}
	root, ok := rootIdentName(target)
	return ok && root == name
}

func isUnitIncrementOf(expr ast.Expr, name string) bool {
	bin, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || bin == nil || bin.Op != lexer.TOKEN_PLUS {
		return false
	}
	if id, ok := stripOptimizationParens(bin.Left).(*ast.Ident); ok && id != nil && id.Name == name {
		return isOneLiteral(bin.Right)
	}
	if id, ok := stripOptimizationParens(bin.Right).(*ast.Ident); ok && id != nil && id.Name == name {
		return isOneLiteral(bin.Left)
	}
	return false
}

func isOneLiteral(expr ast.Expr) bool {
	lit, ok := stripOptimizationParens(expr).(*ast.IntLit)
	return ok && lit != nil && lit.Value == "1"
}
