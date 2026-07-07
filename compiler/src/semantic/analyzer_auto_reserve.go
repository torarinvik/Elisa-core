package semantic

import (
	"os"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// autoReserveDisabledSem opts out of analysis-time auto-reservation.
var autoReserveDisabledSem = os.Getenv("ELISA_NO_AUTO_RESERVE") != ""

// maybeAutoReserveIterFill infers a presize for a `for x in src:` loop that fills a darray, by
// synthesizing `ys.reserve(src.<count>)` and emitting it before the loop (region inference, Phase A).
// The count field is `count` for a darray source and `len` for a borrowed view source. Fixed-size
// list pushes scale the bound (`ys.push([a, b])` reserves `src.<count> * 2`). The darray then never
// reallocates during the fill, and becomes a fixed-footprint citizen that packs densely.
//
// Pure optimization, so the eligibility bar is conservative for safety:
//   - source is a bare identifier of darray or view type — its element count (`.count`/`.len`) is
//     O(1) and re-reading it cannot double-evaluate a side effect (the loop reads the same identifier).
//   - the body grows exactly ONE distinct darray ys via push/extend (in scope, not the source);
//     ambiguous or zero targets are skipped. Over-reserving (e.g. under a `where` filter) is safe.
func (a *Analyzer) maybeAutoReserveIterFill(stmt *ast.IterForStmt, sourceType Type) {
	if autoReserveDisabledSem || stmt == nil || ast.HasSynthesizedPreReserveStmts(stmt) {
		return
	}
	if semanticCloneReserveBoundExpr(stmt.Source) == nil {
		return
	}
	// The source must expose an O(1) element count readable as a plain field: `darray.count` or
	// `view.len`. Both are stable to re-read (the loop reads the same source identifier), so the
	// synthesized `reserve(src.<field>)` cannot double-evaluate a side effect.
	countField := iterSourceCountFieldName(sourceType)
	if countField == "" {
		return
	}
	sourceName := ""
	if srcIdent, ok := stmt.Source.(*ast.Ident); ok {
		sourceName = srcIdent.Name
	}
	preReserves := []ast.Stmt{}
	provenGrowth := collectGrowthTargetCounts(stmt.Body)
	for name, growth := range provenGrowth {
		if sourceName != "" && name == sourceName {
			continue
		}
		sym, ok := a.currentScope.Lookup(name)
		if !ok {
			continue
		}
		if !isDArrayTypeMaybeRef(sym.Type) {
			continue
		}
		bound := semanticIterSourceCountBound(stmt, countField)
		if bound == nil {
			continue
		}
		if !semanticReserveExprIsIntOne(growth) {
			bound = semanticMultiplyReserveExpr(bound, growth, stmt.Position)
		}
		if !a.reserveBoundResolvableHere(bound) {
			continue
		}
		preReserves = append(preReserves, semanticMakeReserveStmt(stmt.Position, name, bound))
	}
	if len(preReserves) == 0 {
		a.lintUninferredAutoReserveIterFill(stmt, sourceName)
		return
	}
	for _, preReserve := range preReserves {
		a.analyzeStmt(preReserve)
	}
	if len(preReserves) == 1 {
		stmt.PreReserve = preReserves[0]
		return
	}
	stmt.PreReserves = preReserves
}

func semanticIterSourceCountBound(stmt *ast.IterForStmt, countField string) ast.Expr {
	if stmt == nil || countField == "" {
		return nil
	}
	sourceClone := semanticCloneReserveBoundExpr(stmt.Source)
	if sourceClone == nil {
		return nil
	}
	return &ast.FieldExpr{Position: stmt.Position, Object: sourceClone, Field: countField}
}

// iterSourceCountFieldName returns the O(1) element-count field for a `for x in src:` source whose
// length is a plain, re-readable field — `count` for a darray, `len` for a borrowed view — or "" for
// any other source (dict/set/range/lazy), which keeps the conservative no-auto-reserve behavior.
func iterSourceCountFieldName(t Type) string {
	if r, ok := t.(*RefType); ok {
		t = r.Elem
	}
	switch StripAggregateStateType(t).(type) {
	case *DArrayType:
		return "count"
	case *ViewType:
		return "len"
	default:
		return ""
	}
}

func (a *Analyzer) maybeAutoReserveCountingFill(stmt *ast.ForStmt) {
	if autoReserveDisabledSem || stmt == nil || ast.HasSynthesizedPreReserveStmts(stmt) {
		return
	}
	loopBound, ok := semanticCountingLoopBoundExpr(stmt)
	if !ok {
		return
	}
	preReserves := []ast.Stmt{}
	for name, growth := range collectGrowthTargetCounts(stmt.Body) {
		sym, ok := a.currentScope.Lookup(name)
		if !ok || !isDArrayTypeMaybeRef(sym.Type) {
			continue
		}
		bound := semanticCloneReserveBoundExpr(loopBound)
		if bound == nil {
			continue
		}
		if !semanticReserveExprIsIntOne(growth) {
			bound = semanticMultiplyReserveExpr(bound, growth, stmt.Pos())
		}
		if !a.reserveBoundResolvableHere(bound) {
			continue
		}
		preReserves = append(preReserves, semanticMakeReserveStmt(stmt.Position, name, bound))
	}
	if len(preReserves) == 0 {
		return
	}
	for _, preReserve := range preReserves {
		a.analyzeStmt(preReserve)
	}
	if len(preReserves) == 1 {
		stmt.PreReserve = preReserves[0]
		return
	}
	stmt.PreReserves = preReserves
}

// reserveBoundResolvableHere reports whether every identifier in a synthesized reserve
// bound resolves in the CURRENT scope — the point just before the loop where the
// `ys.reserve(bound)` statement is emitted. A bound lifted out of a NESTED counting loop
// (collectGrowthTargetCounts multiplies the inner loop's `0..<n` bound into the growth)
// may reference outer-loop BODY locals — `n` in
//
//	for i in 0..<xs.count:
//	    n: usize = i + 1
//	    for j in 0..<n:        # inner bound `n` does not exist before the outer loop
//	        out.push(...)
//
// Hoisting such a bound produced `out.reserve(xs.count * n)` before the outer loop and
// errored "undefined identifier n" (surfacing as an unknown-identifier LLVM-lowering
// crash under -permissive). An unresolvable bound skips the reserve — the target falls
// back to the ordinary grow-on-push path and, for iter fills, the perf lint.
func (a *Analyzer) reserveBoundResolvableHere(bound ast.Expr) bool {
	resolvable := true
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !resolvable || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok {
				if _, ok := a.currentScope.Lookup(ident.Name); !ok {
					resolvable = false
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
	walk(reflect.ValueOf(bound))
	return resolvable
}

func semanticMakeReserveStmt(pos lexer.Pos, target string, bound ast.Expr) ast.Stmt {
	return &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: target}, Field: "reserve"},
		Args:     []ast.Expr{bound},
	}}
}

func (a *Analyzer) lintUninferredAutoReserveIterFill(stmt *ast.IterForStmt, srcName string) {
	target := ""
	for name := range collectGrowthTargetNames(stmt.Body) {
		if name == srcName {
			continue
		}
		sym, ok := a.currentScope.Lookup(name)
		if !ok || !isDArrayTypeMaybeRef(sym.Type) {
			continue
		}
		if target != "" {
			return
		}
		target = name
	}
	if target == "" {
		return
	}
	a.perfLint(stmt.Pos(), "cannot infer a safe reserve bound for %q in this loop; growth may reallocate repeatedly. Make the bound provable or add an explicit `%s.reserve(total)` before the loop", target, target)
}

// isDArrayTypeMaybeRef reports whether t is a darray, looking through a single reference wrapper
// (so a `darray&` parameter and a `darray` local both qualify) and any aggregate-state wrapper.
func isDArrayTypeMaybeRef(t Type) bool {
	if r, ok := t.(*RefType); ok {
		t = r.Elem
	}
	_, ok := StripAggregateStateType(t).(*DArrayType)
	return ok
}

// collectGrowthTargetCounts returns syntactically known per-iteration growth for each receiver
// named by `name.push(...)` or `name.extend(...)` calls in body.
func collectGrowthTargetCounts(body []ast.Stmt) map[string]ast.Expr {
	counts := map[string]ast.Expr{}
	disqualified := map[string]bool{}
	var walk func(v reflect.Value, loopDepth int)
	walk = func(v reflect.Value, loopDepth int) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if field, ok := call.Func.(*ast.FieldExpr); ok && (field.Field == "push" || field.Field == "extend") {
					if recv, ok := field.Object.(*ast.Ident); ok {
						if loopDepth > 0 {
							disqualified[recv.Name] = true
							delete(counts, recv.Name)
							return
						}
						if disqualified[recv.Name] {
							return
						}
						counts[recv.Name] = semanticAddReserveExpr(counts[recv.Name], semanticIntReserveExpr(semanticGrowthCallElementCount(field.Field, call), call.Pos()), call.Pos())
						return
					}
				}
			}
			if node, ok := v.Interface().(ast.Node); ok && isLoopStmtNode(node) {
				if loop, ok := node.(*ast.ForStmt); ok && loopDepth == 0 {
					if bound, ok := semanticCountingLoopBoundExpr(loop); ok {
						inner := collectGrowthTargetCounts(loop.Body)
						for name, innerGrowth := range inner {
							if disqualified[name] {
								continue
							}
							counts[name] = semanticAddReserveExpr(counts[name], semanticMultiplyReserveExpr(bound, innerGrowth, loop.Position), loop.Position)
						}
						return
					}
				}
				walk(v.Elem(), loopDepth+1)
				return
			}
			walk(v.Elem(), loopDepth)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i), loopDepth)
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i), loopDepth)
			}
		}
	}
	walk(reflect.ValueOf(body), 0)
	return counts
}

func collectGrowthTargetNames(body []ast.Stmt) map[string]bool {
	names := map[string]bool{}
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
			if call, ok := v.Interface().(*ast.CallExpr); ok {
				if field, ok := call.Func.(*ast.FieldExpr); ok && (field.Field == "push" || field.Field == "extend") {
					if recv, ok := field.Object.(*ast.Ident); ok {
						names[recv.Name] = true
						return
					}
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
	walk(reflect.ValueOf(body))
	return names
}

func semanticGrowthCallElementCount(method string, call *ast.CallExpr) int {
	if method != "push" || call == nil || len(call.Args) != 1 {
		return 1
	}
	list, ok := call.Args[0].(*ast.ListLitExpr)
	if !ok || list == nil || list.Brace || len(list.Elems) == 0 {
		return 1
	}
	return len(list.Elems)
}
