//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func newPrivateFreshTestScan() *privateFreshScan {
	return &privateFreshScan{declCount: map[string]int{}, fresh: map[string]bool{}, escaped: map[string]bool{}}
}

func TestPrivateFreshScanSharedArguments(t *testing.T) {
	decl := &ast.VarDeclStmt{Name: "buffer", Value: &ast.ListLitExpr{}}
	var expr ast.Expr = &ast.ExprBlock{Stmts: []ast.Stmt{decl}, Value: &ast.BoolLit{Value: true}}
	for i := 0; i < 128; i++ {
		expr = &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
	}
	scan := newPrivateFreshTestScan()
	scan.scanExpr(expr)
	if scan.declCount["buffer"] != 1 || !scan.fresh["buffer"] || scan.escaped["buffer"] {
		t.Fatalf("shared declaration classification: count=%d fresh=%v escaped=%v", scan.declCount["buffer"], scan.fresh["buffer"], scan.escaped["buffer"])
	}
	scan.scanValue(reflect.ValueOf(&ast.VarDeclStmt{Name: "buffer", Value: &ast.ListLitExpr{}}))
	if scan.declCount["buffer"] != 2 {
		t.Fatal("distinct declarations were collapsed")
	}
}

func TestPrivateFreshScanKeepsEscapeModesSeparate(t *testing.T) {
	ident := &ast.Ident{Name: "buffer"}
	field := &ast.FieldExpr{Object: ident, Field: "count"}
	scan := newPrivateFreshTestScan()
	scan.scanExpr(&ast.AddrOfExpr{Operand: ident})
	scan.scanExpr(field)
	if scan.escaped["buffer"] {
		t.Fatal("place access escaped buffer")
	}
	var expr ast.Expr = field
	for i := 0; i < 128; i++ {
		expr = &ast.CallExpr{Func: &ast.Ident{Name: "ident"}, Args: []ast.Expr{expr}, ResolvedArgsValid: true, ResolvedArgs: []ast.Expr{expr}}
	}
	scan.markAllNamesEscaped(reflect.ValueOf(expr))
	if !scan.escaped["buffer"] {
		t.Fatal("value traversal suppressed broader escape mode")
	}
	scan = newPrivateFreshTestScan()
	scan.scanPlace(ident)
	scan.scanExpr(ident)
	if !scan.escaped["buffer"] {
		t.Fatal("place traversal suppressed later whole-value escape")
	}
}

func TestCallArgDisjointBorrowedHeaderRebind(t *testing.T) {
	source := disjointKernelSrc + `
def replace(p: mutable darray[f64]&, q: darray[f64]) -> void:
    p <- q

def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    replace(&a, b)
    axpy(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "disjoint_borrow_rebind.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("regression fixture must be accepted: %v", result.Errors())
	}
	if info := firstDisjointInfo(result); info != nil && info.PairDistinct(0, 1) {
		t.Fatal("accepted helper header rebind must not leave fresh-local disjointness evidence")
	}
}

func TestCallArgDisjointHeaderAliasLaundering(t *testing.T) {
	for name, body := range map[string]string{
		"stored_ref": `
    r: mutable darray[f64]& = &a
    replace(r, b)
    axpy(&a, &b)
`,
		"reassigned_ref": `
    r: mutable darray[f64]& = &b
    r <- &a
    replace(r, b)
    axpy(&a, &b)
`,
	} {
		t.Run(name, func(t *testing.T) {
			source := disjointKernelSrc + `
def replace(p: mutable darray[f64]&, q: darray[f64]) -> void:
    p <- q

def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
` + body
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name+".elisa", source, AnalyzeOptions{})
			if name == "reassigned_ref" {
				if len(result.Errors()) == 0 {
					t.Fatal("short-lived header reference assignment must be rejected")
				}
				return
			}
			if len(result.Errors()) != 0 {
				t.Fatalf("fixture must be accepted: %v", result.Errors())
			}
			if len(result.CallArgDisjoint) != 0 {
				t.Fatalf("aliased buffers retained call disjointness: %+v", result.CallArgDisjoint)
			}
			for decl, info := range result.FuncDisjointParams {
				if decl.Name == "axpy" && len(info.DistinctPairs) != 0 {
					t.Fatal("aliased call grounded function-wide scopes")
				}
			}
		})
	}
}

func TestFuncDisjointTransitiveHeaderRebind(t *testing.T) {
	source := disjointKernelSrc + `
def replace(p: mutable darray[f64]&, q: darray[f64]) -> void:
    p <- q

def wrapper(p: mutable darray[f64]&, q: mutable darray[f64]&, source: darray[f64]) -> void:
    replace(p, source)
    axpy(p, q)

def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    wrapper(&a, &b, b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "transitive_header.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	for decl, info := range result.FuncDisjointParams {
		if (decl.Name == "axpy" || decl.Name == "wrapper") && len(info.DistinctPairs) != 0 {
			t.Fatalf("%s retained function-wide disjointness", decl.Name)
		}
	}
}

func TestPrivateFreshSliceEscapesBacking(t *testing.T) {
	scan := newPrivateFreshTestScan()
	scan.scanValue(reflect.ValueOf(&ast.VarDeclStmt{Name: "buffer", Value: &ast.ListLitExpr{}}))
	scan.scanExpr(&ast.SliceExpr{Object: &ast.Ident{Name: "buffer"}})
	if !scan.escaped["buffer"] {
		t.Fatal("borrowed slice retained a private-fresh backing claim")
	}
}

func TestCallArgDisjointHeaderMutationShapes(t *testing.T) {
	for name, helper := range map[string]string{
		"items": `def replace(p: mutable darray[f64]&, q: darray[f64]) -> void:
    if q.items != null:
        p.items as & <- q.items
`,
		"ufcs": `def replace(self: mutable darray[f64]&, q: darray[f64]) -> void:
    self <- q
`,
	} {
		t.Run(name, func(t *testing.T) {
			call := "replace(&a, b)"
			if name == "ufcs" {
				call = "a.replace(b)"
			}
			source := disjointKernelSrc + helper + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    ` + call + `
    axpy(&a, &b)
`
			result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, name+".elisa", source, AnalyzeOptions{})
			if len(result.Errors()) != 0 {
				t.Fatalf("fixture must be accepted: %v", result.Errors())
			}
			if len(result.CallArgDisjoint) != 0 {
				t.Fatal("header mutation retained disjointness")
			}
		})
	}
}

func TestPrivateFreshStoredBlockBorrowEscapes(t *testing.T) {
	scan := newPrivateFreshTestScan()
	value := &ast.ExprBlock{Value: &ast.AddrOfExpr{Operand: &ast.Ident{Name: "buffer"}}}
	scan.scanValue(reflect.ValueOf(&ast.VarDeclStmt{Name: "alias", Value: value}))
	if !scan.escaped["buffer"] {
		t.Fatal("stored block-result borrow did not escape its root")
	}
}

func TestPrivateFreshBackingPointerEscapes(t *testing.T) {
	scan := newPrivateFreshTestScan()
	scan.scanExpr(&ast.FieldExpr{Object: &ast.Ident{Name: "buffer"}, Field: "items"})
	if !scan.escaped["buffer"] {
		t.Fatal("backing pointer read retained private freshness")
	}
}

func TestContainerBorrowRootSeparatesElementStores(t *testing.T) {
	element := &ast.IndexExpr{Object: &ast.Ident{Name: "buffer"}}
	if containerHeaderRoot(element) != "" {
		t.Fatal("element store classified as header replacement")
	}
	if containerBorrowRoot(element) != "buffer" {
		t.Fatal("element address lost its backing root")
	}
	field := &ast.FieldExpr{Object: element, Field: "value"}
	if containerBorrowRoot(field) != "buffer" {
		t.Fatal("nested element address lost its backing root")
	}
}

func TestPrivateFreshHeaderReferenceCastEscapes(t *testing.T) {
	scan := newPrivateFreshTestScan()
	scan.scanExpr(&ast.CastExpr{Operand: &ast.FieldExpr{Object: &ast.Ident{Name: "buffer"}, Field: "count"}, Target: &ast.RefType{}})
	if !scan.escaped["buffer"] {
		t.Fatal("stored header reference retained private freshness")
	}
}

func TestContainerHeaderStabilityImplicitReferenceReturn(t *testing.T) {
	for _, reference := range []bool{false, true} {
		fn := &ast.FuncDecl{Name: "first", Params: []ast.ParamDecl{{Name: "p"}}, Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IndexExpr{Object: &ast.Ident{Name: "p"}}}}}
		var result Type = &BuiltinType{Name: "i64"}
		if reference {
			result = &RefType{Elem: result}
		}
		a := &Analyzer{funcDeclSymbols: map[*ast.FuncDecl]*Symbol{fn: {Type: &FuncType{Params: []Type{&RefType{Elem: &DArrayType{}}}, Return: result}}}}
		if stable := a.containerHeadersStable(fn); stable == reference {
			t.Fatalf("reference=%v stable=%v", reference, stable)
		}
	}
}

func TestCallArgDisjointBuiltinPushRetainsFreshness(t *testing.T) {
	source := disjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = []
    b: mutable darray[f64] = []
    for i in 0..<8:
        a.push(i.f64())
        b.push(i.f64())
    axpy(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "builtin_push_fresh.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if info := firstDisjointInfo(result); info == nil || !info.PairDistinct(0, 1) {
		t.Fatal("builtin push lost distinctness")
	}
}

func TestCallArgDisjointBackingHeaderCopy(t *testing.T) {
	source := disjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = [1.0]
    b: mutable darray[f64] = zeroed
    if a.items != null:
        b.items as & <- a.items
        b.count <- a.count
        b.capacity <- a.capacity
        axpy(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "backing_header_copy.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if len(result.CallArgDisjoint) != 0 {
		t.Fatal("shared backing headers retained disjointness")
	}
	for decl, info := range result.FuncDisjointParams {
		if decl.Name == "axpy" && len(info.DistinctPairs) != 0 {
			t.Fatal("shared headers grounded function scopes")
		}
	}
}

func TestPrivateFreshAggregateCallBorrowEscapes(t *testing.T) {
	scan := newPrivateFreshTestScan()
	scan.scanExpr(&ast.CallExpr{Func: &ast.Ident{Name: "consume"}, Args: []ast.Expr{&ast.TupleExpr{Elems: []ast.Expr{&ast.AddrOfExpr{Operand: &ast.Ident{Name: "buffer"}}}}}})
	if !scan.escaped["buffer"] {
		t.Fatal("aggregate call retained private backing claim")
	}
}

func TestWholePrivateDArrayBorrowRejectsNestedBuffer(t *testing.T) {
	private := map[string]bool{"outer": true}
	whole := &ast.AddrOfExpr{Operand: &ast.Ident{Name: "outer"}}
	nested := &ast.AddrOfExpr{Operand: &ast.IndexExpr{Object: &ast.Ident{Name: "outer"}}}
	if !wholePrivateDArrayBorrow(whole, private) {
		t.Fatal("whole local lost freshness")
	}
	if wholePrivateDArrayBorrow(nested, private) {
		t.Fatal("outer freshness leaked to inner buffer")
	}
}

func TestCallArgDisjointNestedHeaderCopy(t *testing.T) {
	source := disjointKernelSrc + `
def run() -> void:
    existing: mutable darray[f64] = [1.0]
    outer: mutable darray[darray[f64]] = [existing]
    axpy(&outer[0], &existing)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "nested_header_copy.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if len(result.CallArgDisjoint) != 0 {
		t.Fatal("outer allocation proved inner buffers distinct")
	}
}

func TestCallArgDisjointBuiltinIdentitySourceOrder(t *testing.T) {
	source := disjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = [1.0]
    b: mutable darray[f64] = [2.0]
    axpy(&a, &b)
    a.push(3.0)
    b.reserve(4)
    axpy(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "builtin_identity_order.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if len(result.CallArgDisjoint) != 2 {
		t.Fatalf("both calls need facts: got %d", len(result.CallArgDisjoint))
	}
	for _, info := range result.CallArgDisjoint {
		if !info.PairDistinct(0, 1) {
			t.Fatal("call lost distinctness based on source order")
		}
	}
}

func TestCallArgDisjointAliasBindingCaptured(t *testing.T) {
	source := disjointKernelSrc + `
def run() -> void:
    a: mutable darray[f64] = [1.0]
    r: mutable darray[f64]& = &a
    axpy(&a, r)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "captured_alias.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if len(result.CallArgDisjoint) != 0 {
		t.Fatal("deferred alias lost its call-site roots")
	}
}

func TestCallArgDisjointShadowedParameterIsNotFresh(t *testing.T) {
	source := disjointKernelSrc + `
def run(a: mutable darray[f64], b: mutable darray[f64]) -> void:
    if true:
        a: mutable darray[f64] = []
    axpy(&a, &b)
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "shadowed_parameter.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if len(result.CallArgDisjoint) != 0 {
		t.Fatal("shadowed local allocation supplied freshness for parameter")
	}
}

func TestCallArgDisjointLaterShadowDoesNotChangeBinding(t *testing.T) {
	source := disjointKernelSrc + `
def run(a: mutable darray[f64], b: mutable darray[f64]) -> void:
    if true:
        axpy(&a, &b)
        a: mutable darray[f64] = []
`
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "later_shadow.elisa", source, AnalyzeOptions{})
	if len(result.Errors()) != 0 {
		t.Fatalf("fixture must be accepted: %v", result.Errors())
	}
	if len(result.CallArgDisjoint) != 0 {
		t.Fatal("later shadow changed the captured parameter binding")
	}
}

func TestPrivateFreshSharedDiscardedAndReturnedBuiltin(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "buffer"}, Field: "push"}, Args: []ast.Expr{&ast.BoolLit{Value: true}}}
	scan := newPrivateFreshTestScan()
	scan.analyzer = &Analyzer{compilerBuiltinHelperCalls: map[*ast.CallExpr]string{call: "darray.push"}}
	scan.scanValue(reflect.ValueOf(&ast.ExprStmt{Expr: call}))
	if scan.escaped["buffer"] {
		t.Fatal("discarded builtin escaped private header")
	}
	scan.scanValue(reflect.ValueOf(&ast.ReturnStmt{Value: call}))
	if !scan.escaped["buffer"] {
		t.Fatal("shared returned header hidden by discarded traversal")
	}
}

func TestPrivateFreshSharedBuiltinValueOccurrence(t *testing.T) {
	for _, valueFirst := range []bool{false, true} {
		call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "buffer"}, Field: "push"}}
		scan := newPrivateFreshTestScan()
		scan.analyzer = &Analyzer{compilerBuiltinHelperCalls: map[*ast.CallExpr]string{call: "darray.push"}}
		statement := &ast.ExprStmt{Expr: call}
		value := &ast.LambdaExpr{BodyExpr: call}
		if valueFirst {
			scan.scanExpr(value)
			scan.scanValue(reflect.ValueOf(statement))
		} else {
			scan.scanValue(reflect.ValueOf(statement))
			scan.scanExpr(value)
		}
		if !scan.escaped["buffer"] {
			t.Fatalf("shared value occurrence lost escape, valueFirst=%v", valueFirst)
		}
	}
}

func TestHeaderStabilityCompletedCacheRecursiveClosure(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		aDecl := &ast.FuncDecl{Name: "left", Params: []ast.ParamDecl{{Name: "p"}}}
		bDecl := &ast.FuncDecl{Name: "right", Params: []ast.ParamDecl{{Name: "p"}}}
		call := func(name string) ast.Stmt {
			return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}, Args: []ast.Expr{&ast.Ident{Name: "p"}}}}
		}
		aDecl.Body = []ast.Stmt{call("right")}
		bDecl.Body = []ast.Stmt{call("left")}
		if unsafe {
			aDecl.Body = append(aDecl.Body, &ast.AssignStmt{Target: &ast.Ident{Name: "p"}, Value: &ast.Ident{Name: "p"}})
		}
		a := &Analyzer{globalScope: NewScope(nil), funcDeclSymbols: map[*ast.FuncDecl]*Symbol{}, headerStabilityCompleted: map[*ast.FuncDecl]bool{}}
		for _, fn := range []*ast.FuncDecl{aDecl, bDecl} {
			sym := &Symbol{Name: fn.Name, Node: fn, Type: &FuncType{Params: []Type{&RefType{Elem: &DArrayType{}}}, Return: &BuiltinType{Name: "void"}}}
			a.funcDeclSymbols[fn] = sym
			a.globalScope.Define(sym)
		}
		if stable := a.containerHeadersStable(aDecl); stable == unsafe {
			t.Fatalf("unsafe=%v root stable=%v", unsafe, stable)
		}
		if unsafe {
			if a.headerStabilityCompleted[bDecl] {
				t.Fatal("provisional cycle member cached safe")
			}
		} else if !a.headerStabilityCompleted[bDecl] {
			t.Fatal("completed safe closure not shared")
		}
		if stable := a.containerHeadersStable(bDecl); stable == unsafe {
			t.Fatalf("unsafe=%v second root stable=%v", unsafe, stable)
		}
	}
}
