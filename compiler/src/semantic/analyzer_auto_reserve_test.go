package semantic

import (
	"reflect"
	"strings"
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

func firstForStmt(root any) *ast.ForStmt {
	var found *ast.ForStmt
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
			if loop, ok := v.Interface().(*ast.ForStmt); ok {
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

func reserveBoundArg(t *testing.T, stmt ast.Stmt) ast.Expr {
	t.Helper()
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected reserve prelude to be an expr stmt, got %T", stmt)
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		t.Fatalf("expected reserve call with one bound argument, got %T %#v", exprStmt.Expr, exprStmt.Expr)
	}
	return call.Args[0]
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

func TestAutoReserveCountingLoopSetsPreReserveAfterGap(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(n: usize) -> usize:
    ys: mutable darray[i64] = []
    gap: i64 = 0
    for i in 0..<n:
        ys.push(i.i64() + gap)
    return ys.count
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("no ForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected semantic auto-reserve PreReserve on counting fill loop")
	}
	if ident, ok := reserveBoundArg(t, loop.PreReserve).(*ast.Ident); !ok || ident.Name != "n" {
		t.Fatalf("expected reserve bound n, got %T %#v", reserveBoundArg(t, loop.PreReserve), reserveBoundArg(t, loop.PreReserve))
	}
}

func TestAutoReserveCountingLoopClonesArithmeticAndCountBounds(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> usize:
    ys: mutable darray[i64] = []
    gap: i64 = 0
    for i in 0..<src.count + 1:
        ys.push(i.i64() + gap)
    return ys.count
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("no ForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected semantic auto-reserve for arithmetic count bound")
	}
	if bound, ok := reserveBoundArg(t, loop.PreReserve).(*ast.BinaryExpr); !ok || bound.Op != lexer.TOKEN_PLUS {
		t.Fatalf("expected reserve bound to keep src.count + 1, got %T %#v", reserveBoundArg(t, loop.PreReserve), reserveBoundArg(t, loop.PreReserve))
	}
}

func TestAutoReserveCountingLoopReservesAllProvableTargets(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(n: usize) -> usize:
    xs: mutable darray[i64] = []
    ys: mutable darray[i64] = []
    for i in 0..<n:
        xs.push(i.i64())
        ys.push([i.i64(), i.i64()])
    return xs.count + ys.count
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("no ForStmt found")
	}
	if loop.PreReserve != nil {
		t.Fatalf("multi-target auto-reserve should use PreReserves, got single prelude %T", loop.PreReserve)
	}
	if len(loop.PreReserves) != 2 {
		t.Fatalf("expected two synthesized reserves, got %d", len(loop.PreReserves))
	}
	found := map[string]bool{}
	for _, preReserve := range loop.PreReserves {
		exprStmt, ok := preReserve.(*ast.ExprStmt)
		if !ok {
			t.Fatalf("expected reserve prelude expr stmt, got %T", preReserve)
		}
		call, ok := exprStmt.Expr.(*ast.CallExpr)
		if !ok {
			t.Fatalf("expected reserve call, got %T", exprStmt.Expr)
		}
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok || field.Field != "reserve" {
			t.Fatalf("expected reserve field call, got %T %#v", call.Func, call.Func)
		}
		recv, ok := field.Object.(*ast.Ident)
		if !ok {
			t.Fatalf("expected identifier reserve target, got %T %#v", field.Object, field.Object)
		}
		found[recv.Name] = true
	}
	if !found["xs"] || !found["ys"] {
		t.Fatalf("expected reserves for xs and ys, got %v", found)
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

func TestAutoReserveForInFoldsMultiplePushesPerIteration(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(src: darray[i64]&) -> i64:
    ys: mutable darray[i64] = []
    for x in src:
        ys.push(x)
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
		t.Fatalf("expected reserve bound to multiply source count by folded growth, got %T %#v", call.Args[0], call.Args[0])
	}
	if lit, ok := bound.Right.(*ast.IntLit); !ok || lit.Value != "2" {
		t.Fatalf("expected reserve multiplier 2, got %T %#v", bound.Right, bound.Right)
	}
}

func TestAutoReserveForInPureFieldSourceSetsPreReserve(t *testing.T) {
	file := analyzeAndGetFile(t, `struct Bag:
    items: darray[i64]

def g(bag: Bag&) -> i64:
    ys: mutable darray[i64] = []
    for x in bag.items:
        ys.push(x)
    return ys[0]
`)
	loop := firstIterForStmt(file)
	if loop == nil {
		t.Fatal("no IterForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected an auto-reserve PreReserve on pure field-source for-in fill loop")
	}
	bound := reserveBoundArg(t, loop.PreReserve)
	field, ok := bound.(*ast.FieldExpr)
	if !ok || field.Field != "count" {
		t.Fatalf("expected reserve bound to use source.count, got %T %#v", bound, bound)
	}
	if source, ok := field.Object.(*ast.FieldExpr); !ok || source.Field != "items" {
		t.Fatalf("expected reserve source to clone bag.items, got %T %#v", field.Object, field.Object)
	}
}

func TestAutoReserveForInViewSourceSetsPreReserve(t *testing.T) {
	// A borrowed view source exposes an O(1) `.len`, so a for-in fill over it presizes just like a
	// darray source (which uses `.count`) — closing the gap where view-sourced fills reallocated.
	file := analyzeAndGetFile(t, `def g(src: view[i64]) -> i64:
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
		t.Fatal("expected an auto-reserve PreReserve on the view-source for-in fill loop")
	}
	bound := reserveBoundArg(t, loop.PreReserve)
	field, ok := bound.(*ast.FieldExpr)
	if !ok || field.Field != "len" {
		t.Fatalf("expected reserve bound to use source.len for a view, got %T %#v", bound, bound)
	}
	if source, ok := field.Object.(*ast.Ident); !ok || source.Name != "src" {
		t.Fatalf("expected reserve source to clone the view identifier src, got %T %#v", field.Object, field.Object)
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

func TestAutoReserveForInWarnsWhenBoundCannotBeInferred(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "auto_reserve_uninferred_warn.elisa", `def g(src: darray[darray[i64]]&) -> i64:
    ys: mutable darray[i64] = []
    for chunk in src:
        for x in chunk:
            ys.push(x)
    return ys[0]
`, AnalyzeOptions{})
	all := allDiagnostics(result)
	if !strings.Contains(all, "cannot infer a safe reserve bound") {
		t.Fatalf("expected uninferred auto-reserve warning, got:\n%s", all)
	}
	if len(result.Errors()) != 0 {
		t.Fatalf("default performance lint must be a warning, got errors:\n%v", result.Errors())
	}
}

func TestAutoReserveForInPerfStrictErrorsWhenBoundCannotBeInferred(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "auto_reserve_uninferred_error.elisa", `def g(src: darray[darray[i64]]&) -> i64:
    ys: mutable darray[i64] = []
    for chunk in src:
        for x in chunk:
            ys.push(x)
    return ys[0]
`, AnalyzeOptions{EnforcePerfLints: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "cannot infer a safe reserve bound") {
		t.Fatalf("expected uninferred auto-reserve error, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatal("performance-strict uninferred auto-reserve lint must be an error")
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

func TestAutoReserveForInReservesAllProvableTargets(t *testing.T) {
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
		t.Fatalf("multi-target for-in auto-reserve should use PreReserves, got single prelude %T", loop.PreReserve)
	}
	if len(loop.PreReserves) != 2 {
		t.Fatalf("expected two synthesized reserves, got %d", len(loop.PreReserves))
	}
	found := map[string]bool{}
	for _, preReserve := range loop.PreReserves {
		exprStmt, ok := preReserve.(*ast.ExprStmt)
		if !ok {
			t.Fatalf("expected reserve prelude expr stmt, got %T", preReserve)
		}
		call, ok := exprStmt.Expr.(*ast.CallExpr)
		if !ok {
			t.Fatalf("expected reserve call, got %T", exprStmt.Expr)
		}
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok || field.Field != "reserve" {
			t.Fatalf("expected reserve field call, got %T %#v", call.Func, call.Func)
		}
		recv, ok := field.Object.(*ast.Ident)
		if !ok {
			t.Fatalf("expected identifier reserve target, got %T %#v", field.Object, field.Object)
		}
		found[recv.Name] = true
	}
	if !found["ys"] || !found["zs"] {
		t.Fatalf("expected reserves for ys and zs, got %v", found)
	}
}
