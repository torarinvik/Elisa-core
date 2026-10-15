package semantic

import (
	"reflect"
	"sort"

	"elisacore/src/ast"
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
func (a *Analyzer) havocLoopWrittenFacts(body []ast.Stmt) {
	roots := map[string]bool{}
	for _, stmt := range body {
		collectLoopWrittenRoots(reflect.ValueOf(stmt), roots)
	}
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		target := &ast.Ident{Name: name}
		a.invalidateRangeFactsWithRootPrefix(name)
		a.invalidateWrittenConst(name)
		a.invalidateWrittenFieldsForRoot(name)
		a.invalidatePredFacts(name)
		a.invalidateSMTAssertFactsForTarget(target)
		a.invalidateIndexBoundsReferencingBase(name)
		a.invalidateBoundEqualReferencingBase(name)
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
