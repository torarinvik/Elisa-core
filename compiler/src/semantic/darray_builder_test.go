package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeDArrayBuilderLiteralAndPushSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_builder_push.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs: mutable darray[i64] = []
        xs.push(1)
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
	if len(build.Body) != 2 {
		t.Fatalf("expected alloc binding plus in-block, got %d statements", len(build.Body))
	}
	inStmt, ok := build.Body[1].(*ast.InStoreStmt)
	if !ok {
		t.Fatalf("expected second statement to be in-store, got %T", build.Body[1])
	}
	if len(inStmt.Body) != 3 {
		t.Fatalf("expected var decl, push expr stmt, and return, got %d statements", len(inStmt.Body))
	}
	varDecl, ok := inStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected first in-block statement to be var decl, got %T", inStmt.Body[0])
	}
	literal, ok := varDecl.Value.(*ast.ListLitExpr)
	if !ok {
		t.Fatalf("expected var decl initializer to be list literal, got %T", varDecl.Value)
	}
	literalType, ok := result.ExprTypes[literal].(*DArrayType)
	if !ok || literalType == nil {
		t.Fatalf("expected [] to resolve to darray type, got %T %#v", result.ExprTypes[literal], result.ExprTypes[literal])
	}
	if builtin, ok := literalType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected darray element type i64, got %#v", literalType.Elem)
	}
	exprStmt, ok := inStmt.Body[1].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("expected second in-block statement to be expr stmt, got %T", inStmt.Body[1])
	}
	call, ok := exprStmt.Expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("expected push statement to be call expr, got %T", exprStmt.Expr)
	}
	callType, ok := result.ExprTypes[call].(*RefType)
	if !ok || callType == nil {
		t.Fatalf("expected push call to produce ref type, got %T %#v", result.ExprTypes[call], result.ExprTypes[call])
	}
	if _, ok := callType.Elem.(*DArrayType); !ok {
		t.Fatalf("expected push call ref target to be darray, got %#v", callType.Elem)
	}
}

func TestAnalyzeDArrayBuilderWithArenaScopedAllocatorShorthand(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_with_arena_scoped_allocator.elisa", `def consume(owner: mutable Arena&) -> void:
    pass

def build() -> usize:
    can Memory.Allocate:
        with arena scratch(4096) as owner:
            consume(owner)
            xs: darray[i64] = [1, 2, 3]
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
	canStmt, ok := build.Body[0].(*ast.CanStmt)
	if !ok || len(canStmt.Body) != 1 {
		t.Fatalf("expected can block with scoped arena, got %T %#v", build.Body[0], build.Body[0])
	}
	arena, ok := canStmt.Body[0].(*ast.RegionStmt)
	if !ok || arena.Name != "scratch" || arena.OwnerName != "owner" || len(arena.Body) != 3 {
		t.Fatalf("expected scoped arena shorthand to analyze as region body, got %T %#v", canStmt.Body[0], canStmt.Body[0])
	}
}

func TestAnalyzeRejectsDArrayPushOutsideArenaScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_push_requires_scope.elisa", `def build() -> void:
    xs: mutable darray[i64] = []
    xs.push(1)
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `darray push requires an active in <arena>: scope`) {
		t.Fatalf("expected darray push scope diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeListComprehensionExpr(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_list_comprehension.elisa", `def build(owner: Arena, items: darray[i64]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs = [item + 1 for item in items if item > 0]
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
	varDecl, ok := inStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl, got %T", inStmt.Body[0])
	}
	comp, ok := varDecl.Value.(*ast.ListComprehensionExpr)
	if !ok {
		t.Fatalf("expected list comprehension initializer, got %T", varDecl.Value)
	}
	compType, ok := result.ExprTypes[comp].(*DArrayType)
	if !ok || compType == nil {
		t.Fatalf("expected list comprehension to resolve to darray type, got %T %#v", result.ExprTypes[comp], result.ExprTypes[comp])
	}
	if builtin, ok := compType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected darray element type i64, got %#v", compType.Elem)
	}
	if _, ok := comp.Filter.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected comprehension filter binary expr, got %T", comp.Filter)
	}
	if _, ok := comp.Value.(*ast.BinaryExpr); !ok {
		t.Fatalf("expected comprehension value binary expr, got %T", comp.Value)
	}
}

func TestAnalyzeListComprehensionExprOverRange(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_list_comprehension_range.elisa", `def build(owner: Arena, count: usize) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        xs = [index for index in 1..<count]
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
	inStmt := build.Body[1].(*ast.InStoreStmt)
	varDecl := inStmt.Body[0].(*ast.VarDeclStmt)
	comp, ok := varDecl.Value.(*ast.ListComprehensionExpr)
	if !ok {
		t.Fatalf("expected list comprehension initializer, got %T", varDecl.Value)
	}
	compType, ok := result.ExprTypes[comp].(*DArrayType)
	if !ok || compType == nil {
		t.Fatalf("expected range list comprehension to resolve to darray type, got %T %#v", result.ExprTypes[comp], result.ExprTypes[comp])
	}
	if builtin, ok := compType.Elem.(*BuiltinType); !ok || builtin.Name != "usize" {
		t.Fatalf("expected darray element type usize, got %#v", compType.Elem)
	}
}

func TestAnalyzeQueryExprFamily(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "query_expr_family.elisa", `def has_positive(items: darray[i64]) -> bool:
    return any item in items where item > 0

def all_positive(items: darray[i64]) -> bool:
    return all item in items where item > 0

def first_positive(items: darray[i64]) -> i64?:
    return first item in items where item > 0

def positive_count(items: darray[i64]) -> usize:
    return count item in items where item > 0
`)

	seen := map[ast.QueryExprKind]bool{}
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || len(fn.Body) == 0 {
			continue
		}
		ret, ok := fn.Body[0].(*ast.ReturnStmt)
		if !ok {
			continue
		}
		query, ok := ret.Value.(*ast.QueryExpr)
		if !ok {
			continue
		}
		seen[query.Kind] = true
		switch query.Kind {
		case ast.QueryExprAny, ast.QueryExprAll:
			if builtin, ok := result.ExprTypes[query].(*BuiltinType); !ok || builtin.Name != "bool" {
				t.Fatalf("expected bool query type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
			}
		case ast.QueryExprFirst:
			opt, ok := result.ExprTypes[query].(*OptionalType)
			if !ok || opt == nil {
				t.Fatalf("expected first query to resolve to optional type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
			}
			if builtin, ok := opt.Value.(*BuiltinType); !ok || builtin.Name != "i64" {
				t.Fatalf("expected first query optional payload i64, got %#v", opt.Value)
			}
		case ast.QueryExprCount:
			if builtin, ok := result.ExprTypes[query].(*BuiltinType); !ok || builtin.Name != "usize" {
				t.Fatalf("expected count query type usize, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
			}
		}
	}
	for _, kind := range []ast.QueryExprKind{ast.QueryExprAny, ast.QueryExprAll, ast.QueryExprFirst, ast.QueryExprCount} {
		if !seen[kind] {
			t.Fatalf("missing analyzed query kind %v", kind)
		}
	}
}

func TestAnalyzeFirstProjectionQueryExpr(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "first_projection_query.elisa", `struct Entry:
    name: i64
    enabled: bool

def first_enabled(entries: darray[Entry]) -> i64?:
    return entry.name for first entry in entries where entry.enabled
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "first_enabled" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected first_enabled function declaration")
	}
	ret := fn.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok || query.Projection == nil {
		t.Fatalf("expected projection query expr, got %T %#v", ret.Value, ret.Value)
	}
	opt, ok := result.ExprTypes[query].(*OptionalType)
	if !ok || opt == nil {
		t.Fatalf("expected projection query to resolve to optional type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if builtin, ok := opt.Value.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected projection query optional payload i64, got %#v", opt.Value)
	}
}

func TestAnalyzeRejectsListComprehensionOutsideArenaScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "list_comprehension_requires_scope.elisa", `def build(items: darray[i64]) -> darray[i64]:
    return [item + 1 for item in items]
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `list comprehension requires an active in <arena>: scope`) {
		t.Fatalf("expected list comprehension scope diagnostic, got:\n%s", all)
	}
}

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
