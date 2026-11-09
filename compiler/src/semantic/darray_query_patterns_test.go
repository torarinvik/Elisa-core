package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
)

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

func TestAnalyzeListComprehensionExprWithExplicitOwner(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_list_comprehension_explicit_owner.elisa", `def build(owner: Arena, items: darray[i64]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    xs: darray[i64] @alloc = [item + 1 for item in items if item > 0]
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
	varDecl, ok := build.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected var decl, got %T", build.Body[1])
	}
	comp, ok := varDecl.Value.(*ast.ListComprehensionExpr)
	if !ok {
		t.Fatalf("expected list comprehension initializer, got %T", varDecl.Value)
	}
	// The allocation owner is now carried by the binding type (`darray[i64] @alloc`),
	// not by the comprehension node, since the `[...] in <owner>` expression form was
	// removed (docs/68 §7). The comprehension still resolves to its darray type.
	compType, ok := result.ExprTypes[comp].(*DArrayType)
	if !ok || compType == nil {
		t.Fatalf("expected list comprehension to resolve to darray type, got %T %#v", result.ExprTypes[comp], result.ExprTypes[comp])
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

func TestAnalyzeEachProjectionQueryExpr(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_projection_query.elisa", `struct Entry:
    name: i64
    enabled: bool

def enabled_names(owner: Arena, entries: darray[Entry]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return entry.name for each entry in entries
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "enabled_names" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected enabled_names function declaration")
	}
	inStmt := fn.Body[1].(*ast.InStoreStmt)
	ret := inStmt.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok || query.Projection == nil {
		t.Fatalf("expected projection query expr, got %T %#v", ret.Value, ret.Value)
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected projection query to resolve to darray type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected projection query element i64, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeEachProjectionQueryPatternFilter(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_projection_query_pattern.elisa", `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each item in items where Expr.Int(value)
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
	if !ok || query.PatternFilter == nil {
		t.Fatalf("expected pattern-filter projection query expr, got %T %#v", ret.Value, ret.Value)
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected pattern-filter projection query to resolve to darray type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected pattern-filter projection query element i64, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeEachIdentityQueryExpr(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_identity_query.elisa", `def positives(owner: Arena, items: darray[i64]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return each item in items where item > 0
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "positives" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected positives function declaration")
	}
	inStmt := fn.Body[1].(*ast.InStoreStmt)
	ret := inStmt.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok || query.Kind != ast.QueryExprEach || query.Projection != nil {
		t.Fatalf("expected identity each query expr, got %T %#v", ret.Value, ret.Value)
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected identity each query to resolve to darray type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected identity each query element i64, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeEachIdentityQueryPatternFilterGuard(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "each_identity_query_pattern_guard.elisa", `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[Expr]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return each item in items where item is Expr.Int(value): value > 0
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
	if !ok || query.Kind != ast.QueryExprEach || query.PatternFilter == nil || query.Filter == nil || query.Projection != nil {
		t.Fatalf("expected guarded identity each query expr, got %T %#v", ret.Value, ret.Value)
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected guarded identity each query to resolve to darray type, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if enumType, ok := darrayType.Elem.(*EnumType); !ok || enumType.Name != "Expr" {
		t.Fatalf("expected guarded identity each query element Expr, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeSubjectIsQueryPatternFilterNarrowsPayload(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "subject_is_query_pattern.elisa", `enum Expr:
    Int(value: i64)
    Missing

def has_positive(items: darray[Expr]) -> bool:
    return any item in items where item is Expr.Int(value): value > 0
`)

	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if current, ok := decl.(*ast.FuncDecl); ok && current.Name == "has_positive" {
			fn = current
			break
		}
	}
	if fn == nil {
		t.Fatal("expected has_positive function declaration")
	}
	ret := fn.Body[0].(*ast.ReturnStmt)
	query, ok := ret.Value.(*ast.QueryExpr)
	if !ok || query.PatternFilter == nil || query.Filter == nil {
		t.Fatalf("expected subject-is guarded pattern-filter query, got %T %#v", ret.Value, ret.Value)
	}
	if typ := result.ExprTypes[query]; !IsBoolType(typ) {
		t.Fatalf("expected query result bool, got %s", typ)
	}
}

func TestAnalyzeTupleSubjectQueryPatternFilterNarrowsPayload(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "tuple_subject_query_pattern.elisa", `enum Expr:
    Int(value: i64)
    Missing

def has_positive_after_index(items: darray[Expr]) -> bool:
    return any index, item in items.enumerate() where item is Expr.Int(value): value > index

def count_positive_after_index(items: darray[Expr]) -> usize:
    return count index, item in items.enumerate() where item is Expr.Int(value): value > index

def first_positive_after_index(items: darray[Expr]) -> i64?:
    return value for first index, item in items.enumerate() where item is Expr.Int(value): value > index
`)

	for _, name := range []string{"has_positive_after_index", "count_positive_after_index", "first_positive_after_index"} {
		var fn *ast.FuncDecl
		for _, decl := range result.File.Decls {
			if current, ok := decl.(*ast.FuncDecl); ok && current.Name == name {
				fn = current
				break
			}
		}
		if fn == nil {
			t.Fatalf("expected %s function declaration", name)
		}
		ret := fn.Body[0].(*ast.ReturnStmt)
		query, ok := ret.Value.(*ast.QueryExpr)
		if !ok || query.Pattern == nil || query.PatternFilter == nil || query.PatternFilterSubject != "item" {
			t.Fatalf("expected tuple subject pattern-filter query for %s, got %T %#v", name, ret.Value, ret.Value)
		}
	}
}

func TestAnalyzeRejectsUnknownQueryPatternFilterSubject(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "query_pattern_filter_unknown_subject.elisa", `enum Expr:
    Int(value: i64)
    Missing

def has_positive_after_index(items: darray[Expr]) -> bool:
    return any index, item in items.enumerate() where missing is Expr.Int
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `undefined identifier "missing"`) {
		t.Fatalf("expected unknown query pattern subject diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsQueryPayloadBindingOutsideExpressionScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "query_payload_binding_after_expression.elisa", `enum Expr:
    Int(value: i64)
    Missing

def keep(items: darray[Expr]) -> i64:
    _ = any index, item in items.enumerate() where item is Expr.Int(value): value > index
    return value
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `undefined identifier "value"`) {
		t.Fatalf("expected query payload scope diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeRejectsQueryPatternFilterOnWrongTupleFieldType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "query_pattern_filter_wrong_tuple_field.elisa", `enum Expr:
    Int(value: i64)
    Missing

def has_positive_after_index(items: darray[Expr]) -> bool:
    return any index, item in items.enumerate() where index is Expr.Int(value): value > 0
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `nested variant pattern "Expr.Int" requires an enum or const enum payload, got usize`) {
		t.Fatalf("expected wrong tuple-field pattern diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeSubjectIsEachProjectionQueryPatternFilterNarrowsPayload(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "subject_is_each_projection_query_pattern.elisa", `enum Expr:
    Int(value: i64)
    Missing

def ints(owner: Arena, items: darray[Expr]) -> darray[i64]:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    in alloc:
        return value for each item in items where item is Expr.Int(value)
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
	if !ok || query.PatternFilter == nil {
		t.Fatalf("expected subject-is pattern-filter projection query, got %T %#v", ret.Value, ret.Value)
	}
	darrayType, ok := result.ExprTypes[query].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected darray query result, got %T %#v", result.ExprTypes[query], result.ExprTypes[query])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected pattern-filter projection query element i64, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeTupleSubjectIterablePatternFilterNarrowsPayloadInLoopBody(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "tuple_subject_iter_pattern.elisa", `enum Expr:
    Int(value: i64)
    Missing

def sum(items: darray[Expr]) -> i64:
    total: mutable i64 = 0
    for index, item in items.enumerate() where item is Expr.Int(value):
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
		t.Fatalf("expected tuple subject pattern-filter loop, got %T %#v", fn.Body[1], fn.Body[1])
	}
}
