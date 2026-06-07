package semantic

import (
	"os"
	"reflect"
	"strconv"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// autoReserveDisabledSem opts out of analysis-time auto-reservation (the `for x in coll` case).
// Mirrors the parser-side opt-out so ELISA_NO_AUTO_RESERVE disables both halves.
var autoReserveDisabledSem = os.Getenv("ELISA_NO_AUTO_RESERVE") != ""

// maybeAutoReserveIterFill infers a presize for a `for x in src:` loop that fills a darray, by
// synthesizing `ys.reserve(src.count)` and emitting it before the loop (region inference, Phase A;
// the for-in counterpart of the parser's counting-loop auto-reserve). Fixed-size list pushes scale
// the bound (`ys.push([a, b])` reserves `src.count * 2`). The darray then never reallocates during
// the fill, and becomes a fixed-footprint citizen that packs densely.
//
// Pure optimization, so the eligibility bar is conservative for safety:
//   - source is a bare identifier of darray type — `.count` is O(1) and re-reading it cannot
//     double-evaluate a side effect (the loop reads the same identifier).
//   - the body grows exactly ONE distinct darray ys via push/extend (in scope, not the source);
//     ambiguous or zero targets are skipped. Over-reserving (e.g. under a `where` filter) is safe.
func (a *Analyzer) maybeAutoReserveIterFill(stmt *ast.IterForStmt, sourceType Type) {
	if autoReserveDisabledSem || stmt == nil || stmt.PreReserve != nil {
		return
	}
	srcIdent, ok := stmt.Source.(*ast.Ident)
	if !ok {
		return
	}
	if !isDArrayTypeMaybeRef(sourceType) {
		return
	}
	ysName := ""
	var perIteration ast.Expr
	provenGrowth := collectGrowthTargetCounts(stmt.Body)
	for name, growth := range provenGrowth {
		if name == srcIdent.Name {
			continue
		}
		sym, ok := a.currentScope.Lookup(name)
		if !ok {
			continue
		}
		if !isDArrayTypeMaybeRef(sym.Type) {
			continue
		}
		if ysName != "" {
			return // more than one fill target — ambiguous, skip
		}
		ysName = name
		perIteration = growth
	}
	if ysName == "" {
		a.lintUninferredAutoReserveIterFill(stmt, srcIdent.Name)
		return
	}
	pos := stmt.Position
	bound := ast.Expr(&ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: srcIdent.Name}, Field: "count"})
	if !semanticReserveExprIsIntOne(perIteration) {
		bound = semanticMultiplyReserveExpr(bound, perIteration, pos)
	}
	preReserve := &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: ysName}, Field: "reserve"},
		Args:     []ast.Expr{bound},
	}}
	a.analyzeStmt(preReserve)
	stmt.PreReserve = preReserve
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

func semanticCountingLoopBoundExpr(loop *ast.ForStmt) (ast.Expr, bool) {
	if loop == nil || loop.Reverse || loop.Op != lexer.TOKEN_RANGE_LT || loop.Step != nil {
		return nil, false
	}
	if start, ok := loop.Start.(*ast.IntLit); !ok || start.Value != "0" {
		return nil, false
	}
	bound := semanticCloneReserveBoundExpr(loop.End)
	return bound, bound != nil
}

func semanticCloneReserveBoundExpr(e ast.Expr) ast.Expr {
	switch n := e.(type) {
	case *ast.Ident:
		return &ast.Ident{Position: n.Position, Name: n.Name}
	case *ast.IntLit:
		return &ast.IntLit{Position: n.Position, Value: n.Value, Suffix: n.Suffix, IsHex: n.IsHex}
	}
	return nil
}

func semanticIntReserveExpr(value int, pos lexer.Pos) ast.Expr {
	return &ast.IntLit{Position: pos, Value: strconv.Itoa(value)}
}

func semanticReserveExprIsIntOne(e ast.Expr) bool {
	lit, ok := e.(*ast.IntLit)
	return ok && lit.Value == "1"
}

func semanticIntReserveExprValue(e ast.Expr) (int, bool) {
	lit, ok := e.(*ast.IntLit)
	if !ok || lit == nil {
		return 0, false
	}
	value, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	return value, true
}

func semanticAddReserveExpr(left, right ast.Expr, pos lexer.Pos) ast.Expr {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	if leftValue, ok := semanticIntReserveExprValue(left); ok {
		if rightValue, ok := semanticIntReserveExprValue(right); ok {
			return semanticIntReserveExpr(leftValue+rightValue, pos)
		}
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_PLUS, Left: left, Right: right}
}

func semanticMultiplyReserveExpr(left, right ast.Expr, pos lexer.Pos) ast.Expr {
	if semanticReserveExprIsIntOne(left) {
		return right
	}
	if semanticReserveExprIsIntOne(right) {
		return left
	}
	if leftValue, ok := semanticIntReserveExprValue(left); ok {
		if rightValue, ok := semanticIntReserveExprValue(right); ok {
			return semanticIntReserveExpr(leftValue*rightValue, pos)
		}
	}
	return &ast.BinaryExpr{Position: pos, Op: lexer.TOKEN_STAR, Left: left, Right: right}
}
