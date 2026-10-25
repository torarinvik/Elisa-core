//go:build cgo

package semantic

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"elisacore/src/ast"
)

func privateSpecializationFixture(t *testing.T) (*Result, *ast.FuncDecl) {
	t.Helper()
	result := analyzeTreeTestSource(t, "private_specialization.elisa", `struct Record:
    value: i64

def bounce[T](value: T, depth: i64 = 0) -> T:
    return value

def first[T](value: T) -> T:
    return bounce(value)

module Qualified:
    struct Entry:
        value: i64
    def keep(value: Entry) -> Entry:
        return value
`)
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "first" {
			return result, fn
		}
	}
	t.Fatal("missing first")
	return nil, nil
}

func TestPrivateSpecializationOwnsSyntaxAndMatchingFacts(t *testing.T) {
	result, fn := privateSpecializationFixture(t)
	before := copyASTDecl(fn)
	record := result.NamedTypes["Record"]
	product, err := result.SpecializeFunction(fn, []Type{record})
	if err != nil {
		t.Fatal(err)
	}
	if product.Decl == fn || product.Result == result {
		t.Fatal("specialization borrowed canonical syntax/result")
	}
	if !reflect.DeepEqual(fn, before) {
		t.Fatal("specialization changed canonical template")
	}
	original := fn.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	private := product.Decl.Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if private == original || private.Args[0] == original.Args[0] {
		t.Fatal("call syntax not isolated")
	}
	if product.Result.ExprTypes[private] != record {
		t.Fatalf("concrete call type %v, want canonical nominal %v", product.Result.ExprTypes[private], record)
	}
	if len(private.ResolvedArgs) != 2 {
		t.Fatalf("private default argument resolution lost: %d", len(private.ResolvedArgs))
	}
	if product.Result.ExprTypes[private.ResolvedArgs[1]] == nil {
		t.Fatal("private default argument lacks its semantic type")
	}
	if product.Result.specializationCanonical[product.Decl] != fn {
		t.Fatal("missing canonical declaration routing")
	}
	if product.Result.analyzer == result.analyzer {
		t.Fatal("analyzer-backed codegen queries borrow canonical analyzer")
	}
}

func TestPrivateSpecializationNestedMemoUsesCanonicalRoot(t *testing.T) {
	result, fn := privateSpecializationFixture(t)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	args := []Type{result.NamedTypes["i64"]}
	first, err := result.SpecializeFunction(fn, args)
	if err != nil {
		t.Fatal(err)
	}
	nested, err := first.Result.SpecializeFunction(first.Decl, args)
	if err != nil {
		t.Fatal(err)
	}
	if first != nested {
		t.Fatal("private declaration created a second specialization identity")
	}
}

func TestSpecializationSyntaxKeepsResolvedArgumentBacking(t *testing.T) {
	args := []ast.Expr{&ast.IntLit{Value: "1"}, &ast.IntLit{Value: "2"}}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: args, ResolvedArgs: args, ResolvedArgsValid: true}
	fn := &ast.FuncDecl{Name: "first", Body: []ast.Stmt{&ast.ReturnStmt{Value: call}}}
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(fn))
	if err := syntax.validate(); err != nil {
		t.Fatal(err)
	}
	private := syntax.copy(reflect.ValueOf(fn)).Interface().(*ast.FuncDecl).Body[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	replacement := &ast.IntLit{Value: "3"}
	private.Args[0] = replacement
	if private.ResolvedArgs[0] != replacement {
		t.Fatal("resolved argument alias was split")
	}
	if call.Args[0] == replacement {
		t.Fatal("private write reached canonical backing")
	}
}

func TestSpecializationSyntaxDeclinesPartialBackingOverlap(t *testing.T) {
	args := []ast.Expr{&ast.IntLit{Value: "1"}, &ast.IntLit{Value: "2"}, &ast.IntLit{Value: "3"}}
	call := &ast.CallExpr{Func: &ast.Ident{Name: "f"}, Args: args[:2], ResolvedArgs: args[1:], ResolvedArgsValid: true}
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(call))
	if err := syntax.validate(); err == nil || !strings.Contains(err.Error(), "partially overlapping") {
		t.Fatalf("partial backing admission: %v", err)
	}
}

func TestPrivateSpecializationProjectionCoversEveryResultMap(t *testing.T) {
	// A new public node-fact map must be explicitly wired into the private view.
	// Compare pointer authority, including empty maps, with its analyzer source.
	result, fn := privateSpecializationFixture(t)
	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	original, private := reflect.ValueOf(result).Elem(), reflect.ValueOf(product.Result).Elem()
	for i := 0; i < original.NumField(); i++ {
		field := original.Type().Field(i)
		if field.PkgPath != "" || original.Field(i).Kind() != reflect.Map || original.Field(i).IsNil() {
			continue
		}
		if private.Field(i).IsNil() {
			t.Fatalf("published map %s missing from private view", field.Name)
		}
		// Declaration-owned read-only indexes can be shared; node-fact tables cannot.
		if original.Field(i).Type().Key().Implements(astNodeType) && original.Field(i).Pointer() == private.Field(i).Pointer() {
			t.Fatalf("node-fact map %s remains shared", field.Name)
		}
	}
}

func TestPrivateSpecializationSignatureDetachesNestedRecoverySyntax(t *testing.T) {
	value := &ast.IntLit{Value: "1"}
	recovery := &ast.RecoveryClause{Kind: ast.RecoveryValue, Value: value}
	fallback := &ast.TryExpr{Value: &ast.IntLit{Value: "2"}, Recovery: recovery}
	source := &FuncType{ExplicitParamDefaultExprs: []ast.Expr{fallback}}
	destination := new(FuncType)
	context := &specializationContext{syntax: newSpecializationSyntax()}
	reflect.ValueOf(destination).Elem().Set(context.snapshotFuncType(source, destination))
	if context.err != nil {
		t.Fatal(context.err)
	}
	private := destination.ExplicitParamDefaultExprs[0].(*ast.TryExpr)
	if private == fallback || private.Recovery == recovery || private.Recovery.Value == value {
		t.Fatal("nested default/recovery syntax remains canonical")
	}
	private.Recovery.Value = &ast.IntLit{Value: "3"}
	if recovery.Value != value {
		t.Fatal("private recovery mutation reached template")
	}
}

func TestPrivateSpecializationRetractsTemplateSpanAuthority(t *testing.T) {
	result := analyzeTreeTestSource(t, "private_specialization_proof.elisa", `def unproved[T](xs: darray[T]&, i: usize) -> T:
    trusted Unsafe.UncheckedIndex:
        return xs[i]
`)
	fn := result.File.Decls[0].(*ast.FuncDecl)
	exprs := map[ast.Expr]struct{}{}
	collectSpecializedFunctionExprs(fn, exprs)
	var original *ast.IndexExpr
	for expr := range exprs {
		if idx, ok := expr.(*ast.IndexExpr); ok {
			original = idx
		}
	}
	if original == nil {
		t.Fatal("missing original index")
	}
	if result.analyzer.indexBoundsProven == nil {
		result.analyzer.indexBoundsProven = map[*ast.IndexExpr]bool{}
		result.IndexBoundsProven = result.analyzer.indexBoundsProven
	}
	if result.analyzer.indexBoundsProofOwners == nil {
		result.analyzer.indexBoundsProofOwners = map[*ast.IndexExpr]ast.Node{}
	}
	// A type-only overlay must not inherit an unrelated historical template
	// certificate just because a new private access has the same source span.
	result.analyzer.indexBoundsProven[original] = true
	result.analyzer.indexBoundsProofOwners[original] = fn
	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["i32"]})
	if err != nil {
		t.Fatal(err)
	}
	for expr, proven := range product.Result.IndexBoundsProven {
		if proven && expr.Position == original.Position {
			t.Fatalf("template span certificate survived concrete recheck: %p", expr)
		}
	}
	if !result.IndexBoundsProven[original] {
		t.Fatal("private recheck mutated canonical proof map")
	}
}

func TestPrivateSpecializationPreservesOtherDeclarationProofIdentities(t *testing.T) {
	context := &specializationContext{syntax: newSpecializationSyntax()}
	declarations := []ast.Decl{
		&ast.GlobalDecl{Name: "global"},
		&ast.ConstDecl{Name: "constant"},
		&ast.ExternFuncDecl{Name: "external"},
		&ast.StructDecl{Name: "Record"},
	}
	for _, decl := range declarations {
		source := reflect.ValueOf(decl)
		context.prepareFact(source, map[specializationPointer]bool{})
		if context.syntax.originals[decl] {
			t.Fatalf("unrelated declaration %T entered private syntax ownership", decl)
		}
		copied := context.copyFact(source, map[specializationPointer]reflect.Value{}).Interface()
		if copied != decl {
			t.Fatalf("proof owner/origin identity changed for %T", decl)
		}
	}
	target := &ast.FuncDecl{Name: "target", Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.IntLit{Value: "1"}}}}
	context.syntax.prepare(reflect.ValueOf(target))
	if err := context.syntax.validate(); err != nil {
		t.Fatal(err)
	}
	private := context.syntax.copy(reflect.ValueOf(target)).Interface().(*ast.FuncDecl)
	mapped := context.copyFact(reflect.ValueOf(target), map[specializationPointer]reflect.Value{}).Interface()
	if mapped != private {
		t.Fatal("mapped target declaration proof owner was not rekeyed")
	}
}

func TestPrivateSpecializationFunctionAnalysisQueriesUseCurrentBody(t *testing.T) {
	result, fn := privateSpecializationFixture(t)
	template, ok := result.FunctionAnalysis(fn)
	if !ok {
		t.Fatal("missing template analysis")
	}
	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	concrete, ok := product.Result.FunctionAnalyses[product.Decl]
	if !ok {
		t.Fatal("missing private body analysis")
	}
	if concrete == template {
		t.Fatal("fixture did not build a distinct concrete analysis")
	}
	for _, query := range []struct {
		name string
		run  func() (*FunctionAnalysis, bool)
	}{
		{"private declaration", func() (*FunctionAnalysis, bool) { return product.Result.FunctionAnalysis(product.Decl) }},
		{"canonical current declaration", func() (*FunctionAnalysis, bool) { return product.Result.FunctionAnalysis(fn) }},
		{"current function name", func() (*FunctionAnalysis, bool) { return product.Result.FunctionAnalysisByName("first") }},
	} {
		t.Run(query.name, func(t *testing.T) {
			got, ok := query.run()
			if !ok || got != concrete {
				t.Fatal("current-body query returned template/foreign analysis")
			}
		})
	}
	if got, ok := result.FunctionAnalysisByName("first"); !ok || got != template {
		t.Fatal("canonical result changed after specialization")
	}
	var other *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "bounce" {
			other = candidate
		}
	}
	if other == nil {
		t.Fatal("missing unrelated callee")
	}
	unrelated, ok := result.FunctionAnalysis(other)
	if !ok {
		t.Fatal("missing canonical callee analysis")
	}
	// An instantiation may build a private quiet/read view for this callee. Its
	// declaration-based API must still answer canonical program queries exactly.
	if got, ok := product.Result.FunctionAnalysis(other); !ok || got != unrelated {
		t.Fatal("unrelated canonical declaration query changed")
	}
	if got, ok := product.Result.FunctionAnalysisByName("bounce"); !ok || got != unrelated {
		t.Fatal("unrelated canonical name query changed")
	}
}

func TestPrivateSpecializationKeepsCanonicalResolvedTypeNameRows(t *testing.T) {
	result, fn := privateSpecializationFixture(t)
	before := maps.Clone(result.ResolvedTypeNames)
	if len(before) == 0 {
		t.Fatal("fixture must publish canonical type-name rows outside the generic body")
	}
	qualified := false
	for _, name := range before {
		if name == "Qualified.Entry" {
			qualified = true
		}
	}
	if !qualified {
		t.Fatal("module-local Entry did not resolve to its canonical Qualified.Entry name")
	}

	product, err := result.SpecializeFunction(fn, []Type{result.NamedTypes["i64"]})
	if err != nil {
		t.Fatal(err)
	}
	for expr, name := range before {
		got, exists := product.Result.LookupResolvedTypeNames(expr)
		if !exists || got != name {
			t.Fatalf("canonical type-name row disappeared: %T at %v got=%q exists=%v want=%q", expr, expr.Pos(), got, exists, name)
		}
	}
	if !maps.Equal(before, result.ResolvedTypeNames) {
		t.Fatal("private materialization changed canonical type-name rows")
	}
	if reflect.ValueOf(product.Result.ResolvedTypeNames).Pointer() == reflect.ValueOf(result.ResolvedTypeNames).Pointer() {
		t.Fatal("private type-name rows share mutable canonical map backing")
	}
}

func TestPrivateSpecializationEmptyArgsCreatesNoProductOrMemo(t *testing.T) {
	result, fn := privateSpecializationFixture(t)
	before := copyASTDecl(fn)
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()
	for _, args := range [][]Type{nil, {}} {
		product, err := result.SpecializeFunction(fn, args)
		if err != nil || product != nil {
			t.Fatalf("empty semantic arguments created a product: product=%p err=%v", product, err)
		}
		if len(result.shared.bodies) != 0 {
			t.Fatal("empty semantic arguments populated the specialization memo")
		}
	}
	if !reflect.DeepEqual(before, fn) {
		t.Fatal("empty semantic arguments mutated template syntax")
	}
}

func TestSpecializationSyntaxDropsHiddenTailAndSpareCapacity(t *testing.T) {
	backing := make([]ast.Expr, 1<<16)
	visible := &ast.IntLit{Value: "1"}
	hidden := &ast.Ident{Name: "stale_hidden_tail"}
	backing[0], backing[len(backing)-1] = visible, hidden
	call := &ast.CallExpr{Args: backing[:1]}
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(call))
	if err := syntax.validate(); err != nil {
		t.Fatal(err)
	}
	if syntax.originals[hidden] {
		t.Fatal("preflight visited inaccessible stale tail")
	}
	private := syntax.copy(reflect.ValueOf(call)).Interface().(*ast.CallExpr)
	if len(private.Args) != 1 || cap(private.Args) != 1 {
		t.Fatalf("spare capacity retained: len=%d cap=%d", len(private.Args), cap(private.Args))
	}
	if syntax.nodes[hidden] != nil {
		t.Fatal("stale tail syntax was copied")
	}
	if private.Args[0] == visible {
		t.Fatal("visible syntax was not isolated")
	}
}

func TestSpecializationSyntaxUnionsVisibleAliasesBeforeCopy(t *testing.T) {
	backing := make([]ast.Expr, 3, 20)
	for i := range backing {
		backing[i] = &ast.IntLit{Value: "1"}
	}
	call := &ast.CallExpr{Args: backing[:1], ResolvedArgs: backing[:3], ResolvedArgsValid: true}
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(call))
	if err := syntax.validate(); err != nil {
		t.Fatal(err)
	}
	private := syntax.copy(reflect.ValueOf(call)).Interface().(*ast.CallExpr)
	replacement := &ast.IntLit{Value: "2"}
	private.Args = append(private.Args, replacement)
	if private.ResolvedArgs[1] != replacement {
		t.Fatal("append within observed alias union detached")
	}
	if call.ResolvedArgs[1] == replacement {
		t.Fatal("append reached canonical backing")
	}
	if cap(private.Args) != 3 || cap(private.ResolvedArgs) != 3 {
		t.Fatal("unobserved spare capacity retained")
	}
	detached := append(private.ResolvedArgs, &ast.IntLit{Value: "3"})
	detached[0] = replacement
	if private.ResolvedArgs[0] == replacement {
		t.Fatal("append beyond observed union did not detach")
	}
}

func TestSpecializationSyntaxPreservesVisibleInteriorPointer(t *testing.T) {
	backing := make([]ast.Ident, 2, 20)
	backing[1].Name = "visible"
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(backing))
	syntax.prepare(reflect.ValueOf(&backing[1]))
	if err := syntax.validate(); err != nil {
		t.Fatal(err)
	}
	pointer := syntax.copy(reflect.ValueOf(&backing[1])).Interface().(*ast.Ident)
	private := syntax.copy(reflect.ValueOf(backing)).Interface().([]ast.Ident)
	if pointer != &private[1] || pointer == &backing[1] {
		t.Fatal("visible interior identity lost")
	}
	if cap(private) != 2 {
		t.Fatal("interior pointer retained hidden capacity")
	}
}

func TestSpecializationSyntaxDeclinesHiddenInteriorPointer(t *testing.T) {
	backing := make([]ast.Ident, 2, 20)
	syntax := newSpecializationSyntax()
	syntax.prepare(reflect.ValueOf(backing[:1]))
	syntax.prepare(reflect.ValueOf(&backing[1]))
	if err := syntax.validate(); err == nil || !strings.Contains(err.Error(), "beyond visible") {
		t.Fatalf("hidden interior pointer admission: %v", err)
	}
}
