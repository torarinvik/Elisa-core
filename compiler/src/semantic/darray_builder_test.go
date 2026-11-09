package semantic

import (
	"elisacore/src/ast"
	"strings"
	"testing"
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
	result := analyzeFunctionAnalysisTestSource(t, "darray_literal_region_param.elisa", `def build[@r](anchor: darray[i64] @r) -> usize:
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

// A builder local whose only `.push` lives inside a `while` body must still infer its
// element type — the use-scan recurses into nested control-flow bodies, not just the
// declaration's straight-line siblings. (Previously rejected with "empty list literal
// requires an expected ... type" because the loop-nested push was invisible.)
func TestAnalyzeInfersUntypedDArrayFromPushInLoop(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_loop_push.elisa", `def build() -> void:
    item: i64 = 7
    xs = []
    n: mutable usize = 0
    while n < 3:
        xs.push(item)
        n <- n + 1
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `empty list literal requires`) || strings.Contains(all, `cannot infer element type`) {
		t.Fatalf("expected a loop-nested push to infer the darray element type, got:\n%s", all)
	}
	elem := inferredBuilderElemType(t, result, "build", "xs")
	if builtin, ok := elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected inferred darray element i64, got %#v", elem)
	}
}

// Return-type-driven fallback: when no push pins the element type (a computed push the
// syntactic scan can't resolve) but the local is returned from a `-> darray[T]` function,
// T is taken from the return annotation. This makes the canonical loop-builder shape infer
// with no type annotation on the local.
func TestAnalyzeInfersUntypedDArrayFromReturnType(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "darray_untyped_empty_infers_from_return_type.elisa", `def build() -> darray[i64]:
    xs = []
    n: mutable usize = 0
    while n < 3:
        xs.push(n.i64() * 2)
        n <- n + 1
    return xs
`)

	all := strings.Join(result.Errors(), "\n")
	if strings.Contains(all, `empty list literal requires`) || strings.Contains(all, `cannot infer element type`) {
		t.Fatalf("expected the -> darray[i64] return type to pin the builder element type, got:\n%s", all)
	}
	elem := inferredBuilderElemType(t, result, "build", "xs")
	if builtin, ok := elem.(*BuiltinType); !ok || builtin.Name != "i64" {
		t.Fatalf("expected inferred darray element i64, got %#v", elem)
	}
}

// A computed-push builder with NO return type to fall back on stays a clean, teaching
// diagnostic (not a silent miscompile): the use-scan flags it ambiguous and asks for a type.
func TestAnalyzeRejectsUntypedDArrayWithUnresolvableElement(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "darray_untyped_empty_unresolvable.elisa", `def build() -> void:
    xs = []
    n: mutable usize = 0
    while n < 3:
        xs.push(n.i64() * 2)
        n <- n + 1
`)

	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, `cannot infer element type`) {
		t.Fatalf("expected an ambiguous-builder diagnostic asking for a type, got:\n%s", all)
	}
}

// inferredBuilderElemType locates the auto-region-wrapped VarDecl named declName inside
// fnName and returns the element type its empty-literal initializer resolved to.
func inferredBuilderElemType(t *testing.T, result *Result, fnName, declName string) Type {
	t.Helper()
	var regionStmt *ast.RegionStmt
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name == fnName && len(fn.Body) != 0 {
			regionStmt, _ = fn.Body[0].(*ast.RegionStmt)
			break
		}
	}
	if regionStmt == nil {
		t.Fatalf("expected %s body wrapped in an inferred auto region", fnName)
	}
	for _, stmt := range regionStmt.Body {
		vd, ok := stmt.(*ast.VarDeclStmt)
		if !ok || vd.Name != declName {
			continue
		}
		darrayType, ok := result.ExprTypes[vd.Value].(*DArrayType)
		if !ok || darrayType == nil {
			t.Fatalf("expected %s to resolve to an inferred darray, got %T", declName, result.ExprTypes[vd.Value])
		}
		return darrayType.Elem
	}
	t.Fatalf("did not find inferred builder local %q in %s", declName, fnName)
	return nil
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
