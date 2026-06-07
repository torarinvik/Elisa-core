package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func analyzeAndGetFile(t *testing.T, src string) *ast.File {
	t.Helper()
	l := lexer.New("auto_reserve.elisa", []byte(src))
	tokens := l.Tokenize()
	if errs := l.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected lexer errors: %v", errs)
	}
	p := parser.New(tokens)
	file := p.ParseFile("auto_reserve.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("unexpected parse errors: %v", errs)
	}
	AnalyzeWithOptions(file, AnalyzeOptions{})
	return file
}

func firstIterForStmt(root any) *ast.IterForStmt {
	var found *ast.IterForStmt
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if found != nil || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if loop, ok := v.Interface().(*ast.IterForStmt); ok {
				found = loop
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
	walk(reflect.ValueOf(root))
	return found
}

// A `for x in src:` loop over a darray that fills exactly one darray gets an auto-inserted
// `ys.reserve(src.count)` (recorded on the loop's PreReserve slot).
func TestAutoReserveForInSetsPreReserve(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> i64:
    ys: mutable darray[i64] = []
    for x in src:
        ys.push(x)
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected an auto-reserve PreReserve on the for-in fill loop")
	}
}

func TestAutoReserveForInExtendSetsPreReserve(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[darray[i64]]&) -> i64:
    ys: mutable darray[i64] = []
    for chunk in src:
        ys.extend(chunk)
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected an auto-reserve PreReserve on the for-in extend fill loop")
	}
}

func TestAutoReserveForInCountsListPushElements(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> i64:
    ys: mutable darray[i64] = []
    for x in src:
        ys.push([x, x])
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected an auto-reserve PreReserve on the for-in list-push fill loop")
	}
	exprStmt, ok := loop.PreReserve.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected reserve prelude to be an expr stmt, got %T", loop.PreReserve)
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected reserve call with one bound argument, got %T %#v", exprStmt.Expr, exprStmt.Expr)
	}
	bound, ok := call.Args[0].(*ast.BinaryExpr)
	if !ok || bound.Op != lexer.TOKEN_STAR {
		t.Fatalf("expected reserve bound to multiply source count by list length, got %T %#v", call.Args[0], call.Args[0])
	}
	if lit, ok := bound.Right.(*ast.IntLit); !ok || lit.Value != "2" {
		t.Fatalf("expected reserve multiplier 2, got %T %#v", bound.Right, bound.Right)
	}
}

func TestAutoReserveForInSkipsNestedGrowth(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[darray[i64]]&) -> i64:
    ys: mutable darray[i64] = []
    for chunk in src:
        for x in chunk:
            ys.push(x)
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve != nil {
		t.Fatal("nested for-in growth must not auto-reserve only the outer source count")
	}
}

func TestAutoReserveForInInfersNestedCountingGrowthProduct(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&, m: usize) -> i64:
    ys: mutable darray[i64] = []
    for x in src:
        for j in 0..<m:
            ys.push(j.i64())
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected an auto-reserve PreReserve on the nested counting fill loop")
	}
	exprStmt, ok := loop.PreReserve.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected reserve prelude to be an expr stmt, got %T", loop.PreReserve)
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected reserve call with one bound argument, got %T %#v", exprStmt.Expr, exprStmt.Expr)
	}
	outer, ok := call.Args[0].(*ast.BinaryExpr)
	if !ok || outer.Op != lexer.TOKEN_STAR {
		t.Fatalf("expected reserve bound to multiply source count by nested loop bound, got %T %#v", call.Args[0], call.Args[0])
	}
	if _, ok := outer.Left.(*ast.FieldExpr); !ok {
		t.Fatalf("expected left side to be src.count, got %T %#v", outer.Left, outer.Left)
	}
	if ident, ok := outer.Right.(*ast.Ident); !ok || ident.Name != "m" {
		t.Fatalf("expected right side to be m, got %T %#v", outer.Right, outer.Right)
	}
}

// Two darrays filled in one loop is ambiguous (which to presize?) — skipped.
func TestAutoReserveForInSkipsAmbiguousFill(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> i64:
    ys: mutable darray[i64] = []
    zs: mutable darray[i64] = []
    for x in src:
        ys.push(x)
        zs.push(x)
    return ys[0] + zs[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve != nil {
		t.Fatal("ambiguous multi-target fill must not auto-reserve")
	}
}
