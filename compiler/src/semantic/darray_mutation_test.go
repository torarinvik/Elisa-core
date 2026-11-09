package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestAnalyzeDArrayPushSupportsMutableRefReceivers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_push_ref_receiver.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xr: mutable darray[i64]& = (&xs).cast[mutable darray[i64]&]
        xr.push(1)
        return xs.count
`)

	var build *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "build" {
			build = fn
			break
		}
	}
	if build == nil {
		t.Fatal("expected build function declaration")
	}
	inStmt, ok := build.Body[1].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in-store statement, got %T", build.Body[1])
	}
	exprStmt, ok := inStmt.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected push expr stmt, got %T", inStmt.Body[2])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected push statement to be call expr, got %T", exprStmt.Expr)
	}
	callType, ok := result.ExprTypes[call].(*RefType)
	if !ok || callType == nil {
		t.Fatalf("expected push call to produce ref type, got %T %#v", result.ExprTypes[call], result.ExprTypes[call])
	}
	if !callType.Mutable {
		t.Fatalf("expected push call ref type to remain mutable, got %#v", callType)
	}
}

func TestAnalyzeDArrayPushAcceptsArrayLiteralAsBulkAppend(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_push_array_literal.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[u8] = []
        xs.push([10, 20, 30])
        return xs.count
`)
	var call *ast.CallExpr
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "build" {
			continue
		}
		inStmt := fn.Body[1].(*ast.InStoreStmt)
		exprStmt := inStmt.Body[1].(*ast.ExprStmt)
		call = exprStmt.Expr.(*ast.CallExpr)
		break
	}
	if call == nil {
		t.Fatal("expected darray push call")
	}
	fnType, ok := result.ExprTypes[call.Func].(*FuncType)
	if !ok || fnType == nil || len(fnType.Params) != 2 {
		t.Fatalf("expected darray push func type with source param, got %T %#v", result.ExprTypes[call.Func], result.ExprTypes[call.Func])
	}
	arrayType, ok := fnType.Params[1].(*ArrayType)
	if !ok || arrayType == nil {
		t.Fatalf("expected push array-literal overload to use array param, got %T %#v", fnType.Params[1], fnType.Params[1])
	}
	if arrayType.ConstSize != 3 {
		t.Fatalf("expected fixed array size 3, got %d", arrayType.ConstSize)
	}
	if builtin, ok := arrayType.Elem.(*BuiltinType); !ok || builtin.Name != "u8" {
		t.Fatalf("expected fixed array elem u8, got %#v", arrayType.Elem)
	}
}

func TestAnalyzeDArrayBulkAppendRejectsAffineElements(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_bulk_affine_reject.elisa", `affine struct Tok:
    n: i64

def build(owner: Arena) -> void:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[Tok] = []
        xs.push([Tok(1), Tok(2)])
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "bulk darray push does not support affine element type") {
		t.Fatalf("expected affine bulk push rejection, got:\n%s", all)
	}
}

func TestAnalyzeDArrayExtendSupportsRefReceiversAndArraySources(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_extend_ref_receiver.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[int] = []
        xr: mutable darray[int]& = (&xs).cast[mutable darray[int]&]
        xr.extend([1, 2, 3])
        return xs.count
`)

	var build *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "build" {
			build = fn
			break
		}
	}
	if build == nil {
		t.Fatal("expected build function declaration")
	}
	inStmt, ok := build.Body[1].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in-store statement, got %T", build.Body[1])
	}
	exprStmt, ok := inStmt.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected extend expr stmt, got %T", inStmt.Body[2])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected extend statement to be call expr, got %T", exprStmt.Expr)
	}
	callType, ok := result.ExprTypes[call].(*RefType)
	if !ok || callType == nil || !callType.Mutable {
		t.Fatalf("expected extend call to produce mutable ref type, got %T %#v", result.ExprTypes[call], result.ExprTypes[call])
	}
	list, ok := call.Args[0].(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected extend arg to be list literal, got %T", call.Args[0])
	}
	if _, ok := result.ExprTypes[list].(*ArrayType); !ok {
		t.Fatalf("expected extend list literal to resolve to fixed array, got %T %#v", result.ExprTypes[list], result.ExprTypes[list])
	}
}

func TestAnalyzeDArrayReserveSupportsRefReceivers(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_reserve_ref_receiver.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xr: mutable darray[i64]& = (&xs).cast[mutable darray[i64]&]
		xr.reserve(8)
        return xs.capacity
`)

	var build *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "build" {
			build = fn
			break
		}
	}
	if build == nil {
		t.Fatal("expected build function declaration")
	}
	inStmt, ok := build.Body[1].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in-store statement, got %T", build.Body[1])
	}
	exprStmt, ok := inStmt.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected reserve expr stmt, got %T", inStmt.Body[2])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected reserve statement to be call expr, got %T", exprStmt.Expr)
	}
	callType, ok := result.ExprTypes[call].(*RefType)
	if !ok || callType == nil || !callType.Mutable {
		t.Fatalf("expected reserve call to produce mutable ref type, got %T %#v", result.ExprTypes[call], result.ExprTypes[call])
	}
}

func TestAnalyzeDArrayClearAndTruncate(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_clear_truncate.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[int] = [1, 2, 3]
        xr: mutable darray[int]& = (&xs).cast[mutable darray[int]&]
		xr.truncate(2)
        xr.clear()
        return xs.count
`)

	var build *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "build" {
			build = fn
			break
		}
	}
	if build == nil {
		t.Fatal("expected build function declaration")
	}
	inStmt, ok := build.Body[1].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected in-store statement, got %T", build.Body[1])
	}
	truncStmt, ok := inStmt.Body[2].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected truncate expr stmt, got %T", inStmt.Body[2])
	}
	truncCall, ok := truncStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected truncate statement to be call expr, got %T", truncStmt.Expr)
	}
	if _, ok := result.ExprTypes[truncCall].(*RefType); !ok {
		t.Fatalf("expected truncate call to produce ref type, got %T", result.ExprTypes[truncCall])
	}
	clearStmt, ok := inStmt.Body[3].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected clear expr stmt, got %T", inStmt.Body[3])
	}
	clearCall, ok := clearStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected clear statement to be call expr, got %T", clearStmt.Expr)
	}
	if _, ok := result.ExprTypes[clearCall].(*RefType); !ok {
		t.Fatalf("expected clear call to produce ref type, got %T", result.ExprTypes[clearCall])
	}
}

// Global-container growth binds to the perm (program-lifetime) region: a push/reserve
// on a darray rooted at a global needs no ambient `in <arena>:` scope, because a global
// container is program-lifetime — the in-place analogue of routing a global store's RHS
// to perm. The analyzer records the op in PermGrowthOps so the backend routes it.
func TestAnalyzeGlobalDArrayGrowthBindsToPerm(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "global_darray_growth_perm.elisa", `global mutable g_list: mutable darray[i64] = zeroed

def add(x: i64) -> void:
    can Global{Read, Write}:
        g_list.push(x)
`)
	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) {
		t.Fatalf("expected global darray push to bind to perm without an ambient region, got:\n%s", all)
	}
	if len(result.PermGrowthOps) == 0 {
		t.Fatal("expected the global push to be recorded in PermGrowthOps")
	}
}
