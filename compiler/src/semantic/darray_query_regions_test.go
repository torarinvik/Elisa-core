package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

func TestAnalyzeStructSubjectIterablePatternFilterNarrowsPayloadInLoopBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "struct_subject_iter_pattern.elisa", `enum Expr:
    Int(value: i64)
    Missing

struct Entry:
    index: i64
    item: Expr

def sum(entries: darray[Entry]) -> i64:
    total: mutable i64 = 0
    for {index, item} in entries where item is Expr.Int(value):
        total <- total + value + index
    return total
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "sum" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected sum function declaration")
	}
	loop, ok := fn.Body[1].(*ast.IterForStmt)
	if !ok || loop.PatternFilter == nil || loop.PatternFilterSubject != "item" {
		t.Fatalf("expected struct subject pattern-filter loop, got %T %#v", fn.Body[1], fn.Body[1])
	}
}

func TestAnalyzeEachProjectionQueryPatternFilterGuard(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_projection_query_pattern_guard.elisa", `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each item in items where Expr.Int(value): value > 0
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "ints" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected ints function declaration")
	}
	inStmt := fn.Body[1].(*ast.InStoreStmt)
	ret := inStmt.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok || query.PatternFilter == nil || query.Filter == nil {
		t.Fatalf("expected guarded pattern-filter projection query expr, got %T %#v", ret.Value, ret.Value)
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected guarded pattern-filter projection query to resolve to darray type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected guarded pattern-filter projection query element i64, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeRejectsQueryPatternBindingOutsideFilterScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "query_pattern_binding_scope.elisa", `enum Expr:
    Int(value: i64)
    Missing

def keep(items: darray[Expr]) -> i64:
    if (any item in items where Expr.Int(value)):
        return value
    return 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `undefined identifier "value"`) {
		t.Fatalf("expected pattern-binding scope diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsExpressionWhereTupleSubjectPatternBindingOutsideFilterScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "expr_where_tuple_subject_pattern_scope.elisa", `enum Expr:
    Int(value: i64)
    Missing

def sum(items: darray[Expr]) -> i64:
    total: mutable i64 = 0
    for index, item in (items.enumerate() where index, item is Expr.Int(value): value > index):
        total <- total + value
    return total
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `undefined identifier "value"`) {
		t.Fatalf("expected expression where pattern-binding scope diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeEachProjectionQueryWithExplicitOwner(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_projection_query_explicit_owner.elisa", `struct Entry:
    name: i64

def names(owner: Arena, entries: darray[Entry]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    return entry.name for each entry in entries with alloc
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "names" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected names function declaration")
	}
	ret := fn.Body[1].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok || query.Owner == nil {
		t.Fatalf("expected explicit-owner projection query expr, got %T %#v", ret.Value, ret.Value)
	}
	if _, ok := result.ExprTypes[query].(*DArrayType); !ok {
		t.Fatalf("expected explicit-owner projection query to resolve to darray type, got %T", result.ExprTypes[query])
	}
}

func TestAnalyzeEachQueryUsesExpectedRegionParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_query_region_param.elisa", `def build[@r](items: darray[i64] @r) -> usize:
    out: darray[i64] @r = item + 1 for each item in items
    return out.count
`)
	var query *ast.QueryExpr
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "build" || len(fn.Body) == 0 {
			continue
		}
		varDecl, ok := fn.Body[0].(*ast.VarDeclStmt)
		if !ok {
			t.Fatalf("expected first statement to bind query result, got %T", fn.Body[0])
		}
		query, ok = varDecl.Value.(*ast.QueryExpr)
		if !ok {
			t.Fatalf("expected each query initializer, got %T", varDecl.Value)
		}
		break
	}
	if query == nil {
		t.Fatal("expected build function each query")
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected each query to resolve to darray type, got %T", result.ExprTypes[query])
	}
	if darrayType.Region != "r" {
		t.Fatalf("expected each query result region r, got %q", darrayType.Region)
	}
}

func TestAnalyzeListComprehensionReturnIsRegionPolymorphic(t *testing.T) {
	// A function that returns a region-less comprehension is region-polymorphic (docs/75): the
	// parser wraps its body in a synthesized auto region (so the comprehension has an active
	// scope) and the classifier adopts that region into the caller. No `in <arena>:` ceremony,
	// no threaded allocator — the inferred-region default that lets frontends drop arena params.
	result := analyzeFunctionAnalysisTestSource(t, "list_comprehension_region_poly.elisa", `def build(items: darray[i64]) -> darray[i64]:
    return [item + 1 for item in items]
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `comprehension requires an active in <arena>: scope`) {
		t.Fatalf("comprehension-returning function should be region-polymorphic, got scope error:\n%s", all)
	}
}

func TestAnalyzeListComprehensionUsesExpectedRegionParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "list_comprehension_region_param.elisa", `def build[@r](items: darray[i64] @r) -> usize:
    out: darray[i64] @r = [item + 1 for item in items]
    return out.count
`)
	var comp *ast.ListComprehensionExpr
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "build" || len(fn.Body) == 0 {
			continue
		}
		varDecl, ok := fn.Body[0].(*ast.VarDeclStmt)
		if !ok {
			t.Fatalf("expected first statement to bind comprehension result, got %T", fn.Body[0])
		}
		comp, ok = varDecl.Value.(*ast.ListComprehensionExpr)
		if !ok {
			t.Fatalf("expected list comprehension initializer, got %T", varDecl.Value)
		}
		break
	}
	if comp == nil {
		t.Fatal("expected build function list comprehension")
	}
	darrayType, ok := result.ExprTypes[comp].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected list comprehension to resolve to darray type, got %T", result.ExprTypes[comp])
	}
	if darrayType.Region != "r" {
		t.Fatalf("expected list comprehension result region r, got %q", darrayType.Region)
	}
}

func TestAnalyzeListComprehensionInfersActiveRegion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "list_comprehension_infers_active_region.elisa", `def build(items: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            out = [item + 1 for item in items]
            return out.count
`)
	var comp *ast.ListComprehensionExpr
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "build" || len(fn.Body) == 0 {
			continue
		}
		canStmt, ok := fn.Body[0].(*ast.CanStmt)
		if !ok || len(canStmt.Body) == 0 {
			t.Fatalf("expected can block, got %T", fn.Body[0])
		}
		regionStmt, ok := canStmt.Body[0].(*ast.RegionStmt)
		if !ok || len(regionStmt.Body) == 0 {
			t.Fatalf("expected scoped region block, got %T", canStmt.Body[0])
		}
		varDecl, ok := regionStmt.Body[0].(*ast.VarDeclStmt)
		if !ok {
			t.Fatalf("expected first region statement to bind comprehension result, got %T", regionStmt.Body[0])
		}
		comp, ok = varDecl.Value.(*ast.ListComprehensionExpr)
		if !ok {
			t.Fatalf("expected list comprehension initializer, got %T", varDecl.Value)
		}
		break
	}
	if comp == nil {
		t.Fatal("expected build function list comprehension")
	}
	darrayType, ok := result.ExprTypes[comp].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected list comprehension to resolve to darray type, got %T", result.ExprTypes[comp])
	}
	if darrayType.Region != "a" {
		t.Fatalf("expected list comprehension result region a, got %q", darrayType.Region)
	}
}

func TestAnalyzeEachQueryInfersActiveRegion(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_query_infers_active_region.elisa", `def build(items: darray[i64]) -> usize:
    can Memory.Allocate, Abort.Panic:
        region a(4096):
            out = item + 1 for each item in items
            return out.count
`)
	var query *ast.QueryExpr
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "build" || len(fn.Body) == 0 {
			continue
		}
		canStmt, ok := fn.Body[0].(*ast.CanStmt)
		if !ok || len(canStmt.Body) == 0 {
			t.Fatalf("expected can block, got %T", fn.Body[0])
		}
		regionStmt, ok := canStmt.Body[0].(*ast.RegionStmt)
		if !ok || len(regionStmt.Body) == 0 {
			t.Fatalf("expected scoped region block, got %T", canStmt.Body[0])
		}
		varDecl, ok := regionStmt.Body[0].(*ast.VarDeclStmt)
		if !ok {
			t.Fatalf("expected first region statement to bind query result, got %T", regionStmt.Body[0])
		}
		query, ok = varDecl.Value.(*ast.QueryExpr)
		if !ok {
			t.Fatalf("expected each query initializer, got %T", varDecl.Value)
		}
		break
	}
	if query == nil {
		t.Fatal("expected build function each query")
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected each query to resolve to darray type, got %T", result.ExprTypes[query])
	}
	if darrayType.Region != "a" {
		t.Fatalf("expected each query result region a, got %q", darrayType.Region)
	}
}
