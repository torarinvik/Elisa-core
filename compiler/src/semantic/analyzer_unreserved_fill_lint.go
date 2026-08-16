package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
)

// The unreserved counting-fill lint catches loops semantic auto-reserve cannot prove:
// a provably bounded counting loop grows one darray, but the immediately preceding statement is
// not `xs.reserve(bound)`. The fix is cheap and explicit, and under -Wperf this closes the
// "silent repeated reallocations" path when a harmless statement sits between declaration and fill.
func (a *Analyzer) checkUnreservedCountingFills(fn *ast.FuncDecl) {
	if a == nil || fn == nil || len(fn.Body) == 0 {
		return
	}
	a.findUnreservedCountingFills(fn.Body, nil)
	// Any deferred iter-fill lint whose loop the walker did not reach (a shape it does not
	// descend into) still has to be reported — silence there would lose a real finding rather
	// than honour a remedy. Emitted in source order so diagnostics stay deterministic.
	a.flushRemainingIterFillLints()
}

// precedingReserveCandidate returns the nearest statement before index i that could be an
// explicit reserve, looking THROUGH plain local declarations. A capture list with an
// initializer (`|out, position: usize = 0|`) lowers to a VarDecl between the reserve and the
// loop; a declaration cannot undo a reserve, so it must not hide one.
func precedingReserveCandidate(stmts []ast.Stmt, i int, outerPrev ast.Stmt) ast.Stmt {
	for j := i - 1; j >= 0; j-- {
		if _, isDecl := stmts[j].(*ast.VarDeclStmt); isDecl {
			continue
		}
		return stmts[j]
	}
	return outerPrev
}

// flushPendingIterFillLint emits the deferred "cannot infer a safe reserve bound" for this loop
// unless the statement immediately before it is an explicit `target.reserve(...)` — the remedy
// the message itself advertises. Any bound counts: analysis could not infer one here, so it is
// in no position to second-guess the programmer's (unlike the counting-fill lint above, which
// HAS a proven bound and so requires reserve >= it).
func (a *Analyzer) flushPendingIterFillLint(prev ast.Stmt, loop *ast.IterForStmt) {
	if a == nil || len(a.pendingIterFillLints) == 0 {
		return
	}
	target, ok := a.pendingIterFillLints[loop]
	if !ok {
		return
	}
	delete(a.pendingIterFillLints, loop)
	if a.iterFillLintDecided == nil {
		a.iterFillLintDecided = make(map[*ast.IterForStmt]bool)
	}
	a.iterFillLintDecided[loop] = true
	if stmtIsExplicitReserveFor(prev, target) {
		if a.iterFillSuppressedAt == nil {
			a.iterFillSuppressedAt = make(map[string]bool)
		}
		a.iterFillSuppressedAt[iterFillPosKey(loop)] = true
		return
	}
	a.perfLint(loop.Pos(), "cannot infer a safe reserve bound for %q in this loop; growth may reallocate repeatedly. Make the bound provable or add an explicit `%s.reserve(total)` before the loop", target, target)
}

// iterFillPosKey identifies a loop by source position, so the same loop reached through a
// second AST of the same file is recognised as already decided.
func iterFillPosKey(loop *ast.IterForStmt) string {
	p := loop.Pos()
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// flushRemainingIterFillLints drops deferred lints whose loop the body walk never reached.
// findUnreservedCountingFills descends into EVERY nested statement list, so a survivor means
// this AST's function post-pass did not run over that loop at all (a file reached through a
// second AST does this) — and with no statement list there is no way to tell whether the
// programmer wrote the reserve the message asks for. Emitting blind there resurrected exactly
// the lint a written reserve had already silenced, so an undetermined perf lint stays silent;
// the walked copy of the same loop is the one that decides.
func (a *Analyzer) flushRemainingIterFillLints() {
	if a == nil || len(a.pendingIterFillLints) == 0 {
		return
	}
	a.pendingIterFillLints = make(map[*ast.IterForStmt]string)
}

func (a *Analyzer) findUnreservedCountingFills(stmts []ast.Stmt, outerPrev ast.Stmt) {
	prev := outerPrev
	for i, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ForStmt:
			a.flagUnreservedCountingFill(prev, s)
		case *ast.IterForStmt:
			a.flushPendingIterFillLint(precedingReserveCandidate(stmts, i, outerPrev), s)
		}
		a.descendStmtLists(stmt, prev)
		prev = stmt
	}
}

// descendStmtLists visits every []ast.Stmt nested anywhere inside stmt (skipping stmt itself),
// treating each as its own statement list.
func (a *Analyzer) descendStmtLists(stmt ast.Stmt, outerPrev ast.Stmt) {
	stmtListType := reflect.TypeOf([]ast.Stmt(nil))
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Pointer {
				if seen[v.Pointer()] {
					return
				}
				seen[v.Pointer()] = true
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice:
			if v.Type() == stmtListType && v.CanInterface() {
				a.findUnreservedCountingFills(v.Interface().([]ast.Stmt), outerPrev)
				return
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	// Skip the node itself so its own []ast.Stmt fields are reached, not re-entered.
	rv := reflect.ValueOf(stmt)
	if rv.Kind() == reflect.Pointer && !rv.IsNil() {
		seen[rv.Pointer()] = true
		walk(rv.Elem())
		return
	}
	walk(rv)
}

func (a *Analyzer) flagUnreservedCountingFill(prev ast.Stmt, loop *ast.ForStmt) {
	if ast.HasSynthesizedPreReserveStmts(loop) {
		return
	}
	loopBound, ok := semanticCountingLoopBoundExpr(loop)
	if !ok {
		return
	}
	target := ""
	var perIteration ast.Expr
	growthCounts := collectGrowthTargetCounts(loop.Body)
	for name, growth := range growthCounts {
		if !a.growthTargetIsDArray(loop.Body, name) {
			continue
		}
		if target != "" {
			return
		}
		target = name
		perIteration = growth
	}
	if target == "" {
		return
	}
	expected := loopBound
	if !semanticReserveExprIsIntOne(perIteration) {
		expected = semanticMultiplyReserveExpr(loopBound, perIteration, loop.Pos())
	}
	if stmtIsReserveForBound(prev, target, expected) {
		return
	}
	// Do not demand a reserve that cannot be written. collectGrowthTargetCounts multiplies a
	// NESTED loop's bound into the growth, and that bound may name the flagged loop's own
	// induction variable or a local declared in its body — neither exists at the point the
	// reserve would go, so the suggested `xs.reserve(outer.count * i)` does not compile. This
	// is the same hazard reserveBoundResolvableHere guards for the synthesis path; here it is
	// the ADVICE rather than a synthesized statement, so the lint stays silent instead.
	if exprMentionsAny(expected, namesBoundInsideLoop(loop)) {
		return
	}
	a.perfLint(loop.Pos(), "counting loop grows %q without a matching immediately preceding reserve; add `%s.reserve(%s)` before the loop or keep the fresh darray declaration adjacent so auto-reserve can synthesize it", target, target, optimizationExprString(expected))
}

// namesBoundInsideLoop returns every name a counting loop introduces: its own induction
// variable, plus anything declared or bound anywhere in its body (including nested loops).
// None of these is in scope immediately before the loop.
func namesBoundInsideLoop(loop *ast.ForStmt) map[string]bool {
	names := map[string]bool{}
	if loop == nil {
		return names
	}
	if loop.Name != "" {
		names[loop.Name] = true
	}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.VarDeclStmt:
				if n != nil && n.Name != "" {
					names[n.Name] = true
				}
			case *ast.ForStmt:
				if n != nil && n.Name != "" {
					names[n.Name] = true
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
	walk(reflect.ValueOf(loop.Body))
	return names
}

// exprMentionsAny reports whether expr names any identifier in names.
func exprMentionsAny(expr ast.Expr, names map[string]bool) bool {
	if expr == nil || len(names) == 0 {
		return false
	}
	found := false
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok {
				if names[ident.Name] {
					found = true
				}
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

func (a *Analyzer) growthTargetIsDArray(body []ast.Stmt, name string) bool {
	found := false
	a.walkStaticStmts(body, func(e ast.Expr) bool {
		if found {
			return false
		}
		call, ok := e.(*ast.CallExpr)
		if !ok || call == nil {
			return false
		}
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok || field == nil || (field.Field != "push" && field.Field != "extend") {
			return false
		}
		recv, ok := field.Object.(*ast.Ident)
		if !ok || recv.Name != name {
			return false
		}
		found = isDArrayTypeMaybeRef(a.exprTypes[field.Object])
		return false
	})
	return found
}

// stmtIsExplicitReserveFor reports whether stmt is `name.reserve(<anything>)` — an explicit,
// programmer-written presize for that target, with no requirement on the bound.
func stmtIsExplicitReserveFor(stmt ast.Stmt, name string) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return false
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || call == nil || len(call.Args) != 1 {
		return false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "reserve" {
		return false
	}
	recv, ok := field.Object.(*ast.Ident)
	return ok && recv != nil && recv.Name == name
}

func stmtIsReserveForBound(stmt ast.Stmt, name string, expected ast.Expr) bool {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok || exprStmt == nil {
		return false
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || call == nil {
		return false
	}
	field, ok := call.Func.(*ast.FieldExpr)
	if !ok || field == nil || field.Field != "reserve" {
		return false
	}
	recv, ok := field.Object.(*ast.Ident)
	if !ok || recv.Name != name || len(call.Args) != 1 {
		return false
	}
	return semanticReserveBoundAtLeast(call.Args[0], expected)
}
