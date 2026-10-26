package semantic

import (
	"testing"

	"elisacore/src/ast"
)

const genericProtocolAssociatedTypeSource = `
enum E:
    Leaf(value: i64)

struct BuilderA:
    tag: i64

protocol NodeBuilder:
    type Node
    def make_leaf(value: i64) -> Node

impl NodeBuilder for BuilderA:
    type Node = E

    def make_leaf(value: i64) -> E:
        return E.Leaf(value: value)

def build_core[B: NodeBuilder]() -> B.Node:
    a: B.Node = B.make_leaf(41)
    return a
`

func genericProtocolAssociatedTypeFixture(t *testing.T) (*Result, *ast.FuncDecl, *ast.VarDeclStmt) {
	t.Helper()
	result := analyzeFunctionAnalysisTestSource(t, "generic_protocol_associated_type.elisa", genericProtocolAssociatedTypeSource)
	for _, decl := range result.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name != "build_core" {
			continue
		}
		if len(fn.Body) == 0 {
			t.Fatal("build_core has no body")
		}
		local, ok := fn.Body[0].(*ast.VarDeclStmt)
		if !ok || local.Name != "a" {
			t.Fatalf("expected first build_core statement to declare a, got %T", fn.Body[0])
		}
		return result, fn, local
	}
	t.Fatal("missing build_core")
	return nil, nil, nil
}

func TestSpecializationResolvesConcreteBoundAssociatedType(t *testing.T) {
	result, fn, local := genericProtocolAssociatedTypeFixture(t)
	builder := result.NamedTypes["BuilderA"]
	want := result.NamedTypes["E"]
	if builder == nil || want == nil {
		t.Fatal("fixture types were not registered")
	}

	var resolved Type
	result.analyzer.withSourceGenericParams(fn, fn.GenericParams, []Type{builder}, func() {
		resolved = result.analyzer.resolveType(local.Type)
	})
	if !SameType(resolved, want) {
		t.Fatalf("concrete B.Node resolves to %v, want impl-associated type %v", resolved, want)
	}

	product, err := result.SpecializeFunction(fn, []Type{builder})
	if err != nil {
		t.Fatalf("specialization rejected concrete associated type: %v", err)
	}
	privateLocal, ok := product.Decl.Body[0].(*ast.VarDeclStmt)
	if !ok {
		t.Fatalf("specialized first statement is %T, want local declaration", product.Decl.Body[0])
	}
	if got := product.Result.ExprTypes[privateLocal.Value]; !SameType(got, want) {
		t.Fatalf("specialized protocol call has type %v, want %v", got, want)
	}
}

func TestGenericTemplateKeepsAbstractAssociatedTypeProjection(t *testing.T) {
	result, fn, local := genericProtocolAssociatedTypeFixture(t)
	var resolved Type
	result.analyzer.withSourceGenericParams(fn, fn.GenericParams, nil, func() {
		resolved = result.analyzer.resolveType(local.Type)
	})
	projection, ok := resolved.(*AssociatedTypeProjection)
	if !ok || projection == nil {
		t.Fatalf("abstract B.Node resolved to %T (%v), want associated-type projection", resolved, resolved)
	}
	receiver, ok := projection.Receiver.(*TypeParamType)
	if !ok || receiver == nil || receiver.Name != "B" || projection.InterfaceName != "NodeBuilder" || projection.Name != "Node" {
		t.Fatalf("template projection lost its protocol binder: %+v", projection)
	}
}
