package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

func TestAnalyzeDArrayBuilderLiteralAndPushSugar(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_push.elisa", `def build(owner: Arena) -> usize:
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

func TestAnalyzeDArrayLiteralWithExplicitOwner(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_literal_explicit_owner.elisa", `def build(owner: Arena) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    xs: darray[i64] @alloc = [1, 2, 3]
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
		t.Fatalf("expected explicit-owner darray var decl, got %T", build.Body[1])
	}
	literal, ok := varDecl.Value.(*ast.ListLitExpr)
	if !ok || literal.Owner == nil {
		t.Fatalf("expected list literal with explicit owner, got %T %#v", varDecl.Value, varDecl.Value)
	}
	literalType, ok := result.ExprTypes[literal].(*DArrayType)
	if !ok || literalType == nil {
		t.Fatalf("expected explicit-owner list literal to resolve to darray type, got %T %#v", result.ExprTypes[literal], result.ExprTypes[literal])
	}
}

func TestAnalyzeDArrayLiteralUsesExpectedRegionParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_literal_region_param.elisa", `def build[region r](anchor: darray[i64] @r) -> usize:
    _ = anchor
    xs: darray[i64] @r = [1, 2, 3]
    return xs.count
`)

	var lit *ast.ListLitExpr
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "build" {
			if len(fn.Body) < 2 {
				t.Fatalf("expected binding and darray declaration, got %d statements", len(fn.Body))
			}
			varDecl, ok := fn.Body[1].(*ast.VarDeclStmt)
			if !ok {
				t.Fatalf("expected darray var decl, got %T", fn.Body[1])
			}
			lit, _ = varDecl.Value.(*ast.ListLitExpr)
		}
	}
	if lit == nil {
		t.Fatal("expected darray list literal")
	}
	literalType, ok := result.ExprTypes[lit].(*DArrayType)
	if !ok || literalType == nil {
		t.Fatalf("expected darray literal to resolve to darray type, got %T %#v", result.ExprTypes[lit], result.ExprTypes[lit])
	}
	if literalType.Region != "r" {
		t.Fatalf("expected darray literal region r, got %q", literalType.Region)
	}
}

func TestAnalyzeDArrayLiteralWithSpreadElements(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_literal_spread.elisa", `def build(owner: Arena, first: i64, rest: darray[i64]) -> usize:
    alloc: mutable Arena& = (&owner).cast[mutable Arena&]
    xs: darray[i64] @alloc = [first, ...rest]
    return xs.count
`)

	var lit *ast.ListLitExpr
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			varDecl := fn.Body[1].(*ast.VarDeclStmt)
			lit = varDecl.Value.(*ast.ListLitExpr)
		}
	}
	if lit == nil {
		t.Fatal("expected spread list literal")
	}
	if len(lit.Spreads) != 2 || !lit.Spreads[1] {
		t.Fatalf("expected second list literal element to be spread, got %#v", lit.Spreads)
	}
	if _, ok := result.ExprTypes[lit].(*DArrayType); !ok {
		t.Fatalf("expected spread list literal to resolve to darray type, got %T", result.ExprTypes[lit])
	}
}

func TestAnalyzeRejectsSpreadInArrayLiteral(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "array_literal_spread_rejected.elisa", `def build(rest: i64[2]) -> void:
    xs: i64[3] = [0, ...rest]
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `array literals do not support spread elements`) {
		t.Fatalf("expected array spread diagnostic, got:\n%s", all)
	}
}

// Inference-by-default: a function with a bare allocation (a container literal with no
// `@r` and no explicit region scope, and which doesn't thread an allocator) is wrapped in
// a synthesized lazy auto region, so a region-less `darray = []` + push just works — no
// `region`/`in auto:` annotation required. The old "requires an active in <arena>: scope"
// rejection is exactly what inference now removes.
func TestAnalyzeInfersRegionForBareDArrayPush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_push_infers_region.elisa", `def build() -> void:
    xs: mutable darray[i64] = []
    xs.push(1)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) {
		t.Fatalf("expected inference to supply a region for the bare push, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected safe darray growth to infer no public allocation/panic permission, got %q", got)
	}
}

// Dict parity with darray (the user named "stdlib types like darray and dict"): an empty `{}`
// dict literal is an allocating literal — like `[]` — so a bare `dict[cstr, V] = {}; d.put(...)`
// triggers auto-region inference with NO explicit region scope, and the safe growth infers no
// public allocation/panic permission (the alloc + OOM-panic are trusted stdlib details).
func TestAnalyzeInfersRegionForBareDictBraceLiteralPut(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "dict_brace_infers_region.elisa", `def build() -> void:
    d: mutable dict[cstr, i64] = {}
    d.put("a", 1)
    d.put("b", 2)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) {
		t.Fatalf("expected inference to supply a region for the bare dict put, got:\n%s", all)
	}
	if strings.Contains(all, `brace membership set literals`) {
		t.Fatalf("expected `{}` to be accepted as an empty dict literal, got:\n%s", all)
	}
	sym, ok := result.GlobalScope.Lookup("build")
	if !ok {
		t.Fatal("expected build symbol")
	}
	fnType, ok := sym.Type.(*FuncType)
	if !ok {
		t.Fatalf("expected build function type, got %T", sym.Type)
	}
	if got := PermissionRefsString(fnType.PermissionRefs); got != "" {
		t.Fatalf("expected safe dict growth to infer no public allocation/panic permission, got %q", got)
	}
}

func TestAnalyzeInfersUntypedEmptyDArrayFromPush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_push.elisa", `def build() -> void:
    xs = []
    xs.push([1, 2])
    xs.push(3)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) || strings.Contains(all, `requires a type or initializer`) {
		t.Fatalf("expected untyped empty list plus push to infer a darray, got:\n%s", all)
	}
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == "build" && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil || len(regionStmt.Body) == 0 {
		t.Fatalf("expected function body to be wrapped in inferred auto region, got %#v", result.File.Decls[0])
	}
	varDecl, ok := regionStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected first auto-region statement to be var decl, got %T", regionStmt.Body[0])
	}
	darrayType, ok := result.ExprTypes[varDecl.Value].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected empty literal to resolve to inferred darray type, got %T", result.ExprTypes[varDecl.Value])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "int" {
		t.Fatalf("expected inferred darray element int, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeInfersUntypedDArrayFromVariablePush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_variable_push.elisa", `def build() -> void:
    item: u8 = 7
    xs = []
    xs.push(item)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) || strings.Contains(all, `requires a type or initializer`) {
		t.Fatalf("expected typed push argument to infer an untyped darray, got:\n%s", all)
	}
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == "build" && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil || len(regionStmt.Body) < 2 {
		t.Fatalf("expected function body to be wrapped in inferred auto region, got %#v", result.File.Decls[0])
	}
	varDecl, ok := regionStmt.Body[1].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected inferred darray declaration after typed item, got %T", regionStmt.Body[1])
	}
	darrayType, ok := result.ExprTypes[varDecl.Value].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected empty literal to resolve to inferred darray type, got %T", result.ExprTypes[varDecl.Value])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "u8" {
		t.Fatalf("expected inferred darray element u8, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeInfersUntypedDArrayFromIndexedPush(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_indexed_push.elisa", `def build(src: darray[u8]) -> void:
    xs = []
    xs.push(src[0])
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) || strings.Contains(all, `requires a type or initializer`) {
		t.Fatalf("expected indexed push argument to infer an untyped darray, got:\n%s", all)
	}
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == "build" && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil || len(regionStmt.Body) == 0 {
		t.Fatalf("expected function body to be wrapped in inferred auto region, got %#v", result.File.Decls[0])
	}
	varDecl, ok := regionStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected inferred darray declaration, got %T", regionStmt.Body[0])
	}
	darrayType, ok := result.ExprTypes[varDecl.Value].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected empty literal to resolve to inferred darray type, got %T", result.ExprTypes[varDecl.Value])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "u8" {
		t.Fatalf("expected inferred darray element u8, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeInfersUntypedDArrayFromExtendSource(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_extend_source.elisa", `def build() -> void:
    src: mutable darray[u8] = []
    src.push([1, 2])
    xs = []
    xs.extend(src)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) || strings.Contains(all, `requires a type or initializer`) {
		t.Fatalf("expected typed extend source to infer an untyped darray, got:\n%s", all)
	}
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == "build" && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil || len(regionStmt.Body) < 3 {
		t.Fatalf("expected function body to be wrapped in inferred auto region, got %#v", result.File.Decls[0])
	}
	varDecl, ok := regionStmt.Body[2].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected inferred darray declaration after source setup, got %T", regionStmt.Body[2])
	}
	darrayType, ok := result.ExprTypes[varDecl.Value].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected empty literal to resolve to inferred darray type, got %T", result.ExprTypes[varDecl.Value])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "u8" {
		t.Fatalf("expected inferred darray element u8, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeInfersUntypedDArrayFromExtendParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_extend_param.elisa", `def build(src: darray[u8]) -> void:
    xs = []
    xs.extend(src)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) || strings.Contains(all, `requires a type or initializer`) {
		t.Fatalf("expected typed extend parameter to infer an untyped darray, got:\n%s", all)
	}
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == "build" && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil || len(regionStmt.Body) == 0 {
		t.Fatalf("expected function body to be wrapped in inferred auto region, got %#v", result.File.Decls[0])
	}
	varDecl, ok := regionStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected inferred darray declaration, got %T", regionStmt.Body[0])
	}
	darrayType, ok := result.ExprTypes[varDecl.Value].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected empty literal to resolve to inferred darray type, got %T", result.ExprTypes[varDecl.Value])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "u8" {
		t.Fatalf("expected inferred darray element u8, got %#v", darrayType.Elem)
	}
}

func TestAnalyzeAmbiguousUntypedDArrayBuilderDiagnostic(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_untyped_empty_ambiguous_reserve.elisa", `def build() -> void:
    xs = []
    xs.reserve(10)
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `cannot infer element type for empty darray builder "xs"`) {
		t.Fatalf("expected ambiguous darray builder diagnostic, got:\n%s", all)
	}
	if !strings.Contains(all, `xs: T[] = []`) {
		t.Fatalf("expected typed declaration suggestion without numeric suffixes, got:\n%s", all)
	}
	if strings.Contains(all, `empty list literal requires an expected array or darray type`) {
		t.Fatalf("expected tailored darray builder diagnostic without generic empty-list noise, got:\n%s", all)
	}
}

func TestAnalyzeInfersUntypedNonEmptyDArrayFromUse(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_nonempty_infers_from_use.elisa", `def build() -> void:
    xs = [1, 2]
    xs.push(3)
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `requires an active in <arena>: scope`) || strings.Contains(all, `requires a type or initializer`) {
		t.Fatalf("expected untyped non-empty list plus push to infer a darray, got:\n%s", all)
	}
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == "build" && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil || len(regionStmt.Body) == 0 {
		t.Fatalf("expected function body to be wrapped in inferred auto region, got %#v", result.File.Decls[0])
	}
	varDecl, ok := regionStmt.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("expected first auto-region statement to be var decl, got %T", regionStmt.Body[0])
	}
	darrayType, ok := result.ExprTypes[varDecl.Value].(*DArrayType)
	if !ok || darrayType == nil {
		t.Fatalf("expected non-empty literal to resolve to inferred darray type, got %T", result.ExprTypes[varDecl.Value])
	}
	if builtin, ok := darrayType.Elem.(*BuiltinType); !ok || builtin.Name != "int" {
		t.Fatalf("expected inferred darray element int, got %#v", darrayType.Elem)
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
	result := analyzeFunctionAnalysisTestSource(t, "each_query_region_param.elisa", `def build[region r](items: darray[i64] @r) -> usize:
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

func TestAnalyzeRejectsListComprehensionOutsideArenaScope(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "list_comprehension_requires_scope.elisa", `def build(items: darray[i64]) -> darray[i64]:
    return [item + 1 for item in items]
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `comprehension requires an active in <arena>: scope`) {
		t.Fatalf("expected list comprehension scope diagnostic, got:\n%s", all)
	}
}

func TestAnalyzeListComprehensionUsesExpectedRegionParam(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "list_comprehension_region_param.elisa", `def build[region r](items: darray[i64] @r) -> usize:
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
