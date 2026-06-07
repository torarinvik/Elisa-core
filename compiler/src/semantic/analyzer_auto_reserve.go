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
	perIteration := 0
	for name, growth := range collectGrowthTargetCounts(stmt.Body) {
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
		return
	}
	pos := stmt.Position
	bound := ast.Expr(&ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: srcIdent.Name}, Field: "count"})
	if perIteration > 1 {
		bound = &ast.BinaryExpr{
			Position: pos,
			Op:       lexer.TOKEN_STAR,
			Left:     bound,
			Right:    &ast.IntLit{Position: pos, Value: strconv.Itoa(perIteration)},
		}
	}
	preReserve := &ast.ExprStmt{Position: pos, Expr: &ast.CallExpr{
		Position: pos,
		Func:     &ast.FieldExpr{Position: pos, Object: &ast.Ident{Position: pos, Name: ysName}, Field: "reserve"},
		Args:     []ast.Expr{bound},
	}}
	a.analyzeStmt(preReserve)
	stmt.PreReserve = preReserve
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
func collectGrowthTargetCounts(body []ast.Stmt) map[string]int {
	counts := map[string]int{}
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
						counts[recv.Name] += semanticGrowthCallElementCount(field.Field, call)
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
	return counts
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
