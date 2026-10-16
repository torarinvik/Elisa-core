package semantic

import (
	"reflect"
	"sort"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// havocLoopWrittenFacts drops, before a loop body is analyzed, every fact about a variable the body
// may write. The body is analyzed ONCE, against the facts of the first iteration; a later iteration
// sees the previous iteration's writes, so an entry fact about a written variable does not hold at
// the loop head. Keeping it proved `assert h == 5` (and `invariant h == 5`) inside a loop that
// increments h and elided the runtime check. Invariant establishment, termination and exit-guard
// soundness read the pristine pre-loop scope, so callers run those first.
//
// "May write" is syntactic and conservative: the root of every assignment target, every `&x`
// operand root, and every method-call receiver root (a mutating method or a `&self` callee).
func (a *Analyzer) havocLoopWrittenFacts(body []ast.Stmt, cond ast.Expr) {
	roots := loopWrittenRoots(body)
	counters := monotoneLoopCounters(body, cond)
	kept := map[string]numRange{}
	for name := range counters {
		if r, ok := a.lookupRangeFact(name); ok && r.loKnown {
			kept[name] = numRange{loKnown: true, lo: r.lo}
		}
	}
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)
	elementOnly := loopElementOnlyRoots(body)
	for _, name := range names {
		target := &ast.Ident{Name: name}
		a.invalidateRangeFactsWithRootPrefix(name)
		a.invalidateWrittenConst(name)
		a.invalidateWrittenFieldsForRoot(name)
		a.invalidatePredFacts(name)
		a.invalidateSMTAssertFactsForTarget(target)
		if elementOnly[name] && a.loopRootIsBuiltinIndexable(name) {
			// Element stores (`a[i] <- v`) never change the container's length, so index-bound and
			// length-equality facts about it (`i < a.count`) still hold at every loop head.
			continue
		}
		a.invalidateIndexBoundsReferencingBase(name)
		a.invalidateBoundEqualReferencingBase(name)
	}
	for _, name := range names {
		if r, ok := kept[name]; ok && a.currentScope != nil {
			if a.currentScope.rangeFacts == nil {
				a.currentScope.rangeFacts = map[string]numRange{}
			}
			a.currentScope.rangeFacts[name] = r
		}
	}
}

func collectLoopWrittenRoots(v reflect.Value, roots map[string]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch n := v.Interface().(type) {
		case *ast.AssignStmt:
			addLoopWrittenRoot(n.Target, roots)
		case *ast.AugAssignStmt:
			addLoopWrittenRoot(n.Target, roots)
		case *ast.AsRefAssignStmt:
			addLoopWrittenRoot(n.Target, roots)
		case *ast.AddrOfExpr:
			addLoopWrittenRoot(n.Operand, roots)
		case *ast.CallExpr:
			if field, ok := stripOptimizationParens(n.Func).(*ast.FieldExpr); ok && field != nil {
				addLoopWrittenRoot(field.Object, roots)
			}
		}
		collectLoopWrittenRoots(v.Elem(), roots)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			collectLoopWrittenRoots(v.Field(i), roots)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			collectLoopWrittenRoots(v.Index(i), roots)
		}
	}
}

func addLoopWrittenRoot(target ast.Expr, roots map[string]bool) {
	if name, ok := rootIdentName(stripOptimizationParens(target)); ok && name != "" {
		roots[name] = true
	}
}

// isInductiveLoopInvariant reports whether an in-body `invariant` was already proven inductive
// (established on entry and preserved by the body) by proveLoopInvariants for the enclosing loop. It
// then holds at the head of every iteration, so the in-place check, which sees only the havocked
// loop-head facts, must not re-prove it.
func (a *Analyzer) isInductiveLoopInvariant(stmt *ast.ContractStmt) bool {
	for _, inv := range a.activeOuterLoopInvariants {
		if inv == stmt {
			return true
		}
	}
	return false
}

func loopWrittenRoots(body []ast.Stmt) map[string]bool {
	roots := map[string]bool{}
	for _, stmt := range body {
		collectLoopWrittenRoots(reflect.ValueOf(stmt), roots)
	}
	return roots
}

// exprReadsAnyRoot reports whether expr mentions any identifier in roots (conservatively, any Ident
// node with that name anywhere in the expression tree).
func exprReadsAnyRoot(expr ast.Expr, roots map[string]bool) bool {
	found := false
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil && roots[id.Name] {
				found = true
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(expr))
	return found
}

// monotoneLoopCounters returns the counter a `while x < e` loop only steps upward: the guard is a
// strict `x < e` on a plain identifier, every write to x in the body is `x <- x + 1` (or `x += 1`),
// nothing borrows x or calls a method on it, and no path through one iteration increments it more
// than once (branches take the max, sequences add, a nested loop that writes x disqualifies). Each
// increment then runs with x < e <= max, so it never overflows and x never decreases: its entry
// LOWER bound holds at every loop head (and after the loop). Its upper bound and value do not.
func monotoneLoopCounters(body []ast.Stmt, cond ast.Expr) map[string]bool {
	guard, ok := stripOptimizationParens(cond).(*ast.BinaryExpr)
	if !ok || guard == nil || guard.Op != lexer.TOKEN_LT {
		return nil
	}
	id, ok := stripOptimizationParens(guard.Left).(*ast.Ident)
	if !ok || id == nil {
		return nil
	}
	steps, ok := counterPathSteps(body, id.Name)
	if !ok || steps != 1 {
		return nil
	}
	return map[string]bool{id.Name: true}
}

// counterPathSteps is the most increments of name along any path through stmts, or ok=false when a
// statement writes name any other way.
func counterPathSteps(stmts []ast.Stmt, name string) (int, bool) {
	total := 0
	for _, stmt := range stmts {
		switch n := stmt.(type) {
		case *ast.AssignStmt:
			if loopRootIs(n.Target, name) {
				if !isIdentNamed(n.Target, name) || !isIncrementByOne(n.Value, name) || stmtTouchesCounter(n.Value, name) {
					return 0, false
				}
				total++
				continue
			}
		case *ast.AugAssignStmt:
			if loopRootIs(n.Target, name) {
				if !isIdentNamed(n.Target, name) || n.Op != lexer.TOKEN_PLUSEQ || !isIntLitOne(n.Value) {
					return 0, false
				}
				total++
				continue
			}
		case *ast.IfStmt:
			if stmtTouchesCounter(n.Cond, name) {
				return 0, false
			}
			best, ok := counterPathSteps(n.Then, name)
			if !ok {
				return 0, false
			}
			for _, elif := range n.Elifs {
				if stmtTouchesCounter(elif.Cond, name) {
					return 0, false
				}
				k, ok := counterPathSteps(elif.Body, name)
				if !ok {
					return 0, false
				}
				best = max(best, k)
			}
			k, ok := counterPathSteps(n.Else, name)
			if !ok {
				return 0, false
			}
			total += max(best, k)
			continue
		}
		if stmtTouchesCounter(stmt, name) {
			return 0, false
		}
	}
	return total, true
}

// stmtTouchesCounter reports whether node may write name: an assignment rooted at it, a borrow of
// it, or a method call on it, anywhere inside (nested loops and lambdas included).
func stmtTouchesCounter(node any, name string) bool {
	if node == nil {
		return false
	}
	roots := map[string]bool{}
	collectLoopWrittenRoots(reflect.ValueOf(node), roots)
	return roots[name]
}

func loopRootIs(expr ast.Expr, name string) bool {
	root, ok := rootIdentName(stripOptimizationParens(expr))
	return ok && root == name
}

func isIntLitOne(expr ast.Expr) bool {
	lit, ok := stripOptimizationParens(expr).(*ast.IntLit)
	return ok && lit != nil && lit.Value == "1" && !lit.IsHex
}

func isIncrementByOne(expr ast.Expr, name string) bool {
	b, ok := stripOptimizationParens(expr).(*ast.BinaryExpr)
	if !ok || b == nil || b.Op != lexer.TOKEN_PLUS {
		return false
	}
	return (isIdentNamed(b.Left, name) && isIntLitOne(b.Right)) || (isIntLitOne(b.Left) && isIdentNamed(b.Right, name))
}

// collectExprIdentNames adds the name of every identifier inside expr to out.
func collectExprIdentNames(expr ast.Expr, out map[string]bool) {
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if id, ok := v.Interface().(*ast.Ident); ok && id != nil {
				out[id.Name] = true
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(expr))
}

// deferLoopInvariantLint holds an inductive-proof failure for a leading loop invariant until the
// in-place check at the loop head has run. That check sees the guard and every fact the body cannot
// change (the havocked loop-head scope), so it often proves a clause the separate establishment or
// preservation query could not; the lint is emitted only when both fail.
func (a *Analyzer) deferLoopInvariantLint(inv *ast.ContractStmt, msg string) {
	if a.pendingLoopInvariantLints == nil {
		a.pendingLoopInvariantLints = map[*ast.ContractStmt]string{}
	}
	a.pendingLoopInvariantLints[inv] = msg
}

func (a *Analyzer) canAssumeLoopInvariantInPlace(stmt *ast.ContractStmt) bool {
	msg, pending := a.pendingLoopInvariantLints[stmt]
	if pending {
		delete(a.pendingLoopInvariantLints, stmt)
	}
	if a.canAssumeContractFact(stmt.Cond) {
		return true
	}
	if pending {
		a.proofLint(stmt.Pos(), "%s", msg)
	}
	return false
}

// loopElementOnlyRoots returns the roots whose every write in body is a plain element store
// `root[i] <- v` (no reassignment of the root, no field write, no borrow, no method call on it).
func loopElementOnlyRoots(body []ast.Stmt) map[string]bool {
	element := map[string]bool{}
	other := map[string]bool{}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Interface {
				walk(v.Elem())
				return
			}
			switch n := v.Interface().(type) {
			case *ast.AssignStmt:
				if idx, ok := stripOptimizationParens(n.Target).(*ast.IndexExpr); ok && idx != nil {
					if id, ok := stripOptimizationParens(idx.Object).(*ast.Ident); ok && id != nil {
						element[id.Name] = true
						break
					}
				}
				addLoopWrittenRoot(n.Target, other)
			case *ast.AugAssignStmt:
				addLoopWrittenRoot(n.Target, other)
			case *ast.AsRefAssignStmt:
				addLoopWrittenRoot(n.Target, other)
			case *ast.AddrOfExpr:
				addLoopWrittenRoot(n.Operand, other)
			case *ast.CallExpr:
				if field, ok := stripOptimizationParens(n.Func).(*ast.FieldExpr); ok && field != nil {
					addLoopWrittenRoot(field.Object, other)
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, stmt := range body {
		walk(reflect.ValueOf(stmt))
	}
	for name := range other {
		delete(element, name)
	}
	return element
}

// loopRootIsBuiltinIndexable: a built-in array or darray, whose element store cannot change its
// length (a user type's index-assignment operator could).
func (a *Analyzer) loopRootIsBuiltinIndexable(name string) bool {
	if a.currentScope == nil {
		return false
	}
	sym, ok := a.currentScope.Lookup(name)
	if !ok || sym == nil {
		return false
	}
	if isDArrayTypeMaybeRef(sym.Type) {
		return true
	}
	_, isArray := stripRefForBounds(sym.Type).(*ArrayType)
	return isArray
}
