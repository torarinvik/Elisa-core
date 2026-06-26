//go:build cgo

package semantic

import (
	"testing"

	"elisacore/src/ast"
)

func TestSpecSignatureSplitsRefinementChannels(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0
law Bounded(self: i64, lo: i64, hi: i64) = self >= lo and self <= hi

def shape(x: i64 is Positive, limit: i64) -> i64 is Bounded[0, limit]:
    requires limit > 0
    ensure result >= x
    return x

def touch(p: mutable i64&) -> void ensures p is Positive:
    p <- 1
`
	result := analyzeTreeTestSource(t, "spec_signature_channels.elisa", src)
	sym, ok := result.GlobalScope.Lookup("shape")
	if !ok {
		t.Fatal("expected shape symbol")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft.SpecSignature == nil {
		t.Fatalf("expected shape to have a function type with SpecSignature, got %T", sym.Type)
	}
	sig := ft.SpecSignature
	if got := len(sig.ParamPredicates); got != 1 {
		t.Fatalf("expected one parameter predicate, got %d", got)
	}
	if got := len(sig.ResultPredicates); got != 1 {
		t.Fatalf("expected one result predicate, got %d", got)
	}
	if got := len(sig.Requires); got != 1 {
		t.Fatalf("expected one requires premise, got %d", got)
	}
	if got := len(sig.Ensures); got != 1 {
		t.Fatalf("expected one ensures guarantee, got %d", got)
	}
	if got := len(sig.ParamPostconditionLaws); got != 0 {
		t.Fatalf("value-returning shape should not have param postcondition laws, got %d", got)
	}

	touchSym, ok := result.GlobalScope.Lookup("touch")
	if !ok {
		t.Fatal("expected touch symbol")
	}
	touchType, ok := touchSym.Type.(*FuncType)
	if !ok || touchType.SpecSignature == nil {
		t.Fatalf("expected touch to have a function type with SpecSignature, got %T", touchSym.Type)
	}
	if got := len(touchType.SpecSignature.ParamPostconditionLaws); got != 1 {
		t.Fatalf("expected one param postcondition law, got %d", got)
	}
}

func TestCloneSpecSignatureExportedWrapperPreservesSplitChannels(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def touch(p: mutable i64&) -> i64 ensures p is Positive:
    requires p > 0
    ensure result == 1
    p <- 1
    return 1
`
	result := analyzeTreeTestSource(t, "spec_signature_exported_clone_channels.elisa", src)
	sym, ok := result.GlobalScope.Lookup("touch")
	if !ok {
		t.Fatal("expected touch symbol")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft.SpecSignature == nil {
		t.Fatalf("expected touch to have a function type with SpecSignature, got %T", sym.Type)
	}
	clone := CloneSpecSignature(ft.SpecSignature)
	if clone == nil {
		t.Fatal("expected cloned signature")
	}
	if len(clone.Requires) != 1 || len(clone.Ensures) != 1 || len(clone.ParamPostconditionLaws) != 1 {
		t.Fatalf("exported clone lost split channels: requires=%d ensures=%d paramPostconditionLaws=%d", len(clone.Requires), len(clone.Ensures), len(clone.ParamPostconditionLaws))
	}
	clone.Requires = nil
	clone.Ensures = nil
	clone.ParamPostconditionLaws = nil
	if len(ft.SpecSignature.Requires) != 1 || len(ft.SpecSignature.Ensures) != 1 || len(ft.SpecSignature.ParamPostconditionLaws) != 1 {
		t.Fatal("mutating exported clone should not mutate original signature")
	}
}

func TestExternCallRefinementSchemeFallsBackToSignatureRefinements(t *testing.T) {
	refinedI64 := &ast.RefinementTypeExpr{
		Base: &ast.BuiltinTypeExpr{Name: "i64"},
		Preds: []ast.RefinementPredExpr{{
			Name: "Positive",
		}},
	}
	ext := &ast.ExternFuncDecl{
		Name: "imported",
		Params: []ast.ParamDecl{{
			Name: "x",
			Type: refinedI64,
		}},
		ReturnType: refinedI64,
		Requires:   []ast.Expr{&ast.BoolLit{Value: true}},
	}
	scope := NewScope(nil)
	scope.Define(&Symbol{
		Name: "imported",
		Kind: SymbolFunc,
		Type: &FuncType{Name: "imported"},
		Node: ext,
	})
	a := &Analyzer{currentScope: scope, exprTypes: map[ast.Expr]Type{}}
	scheme, ok := a.callRefinementScheme(&ast.CallExpr{Func: &ast.Ident{Name: "imported"}})
	if !ok {
		t.Fatal("expected extern call refinement scheme")
	}
	if len(scheme.Requires) != 1 {
		t.Fatalf("expected extern requires to survive fallback, got %d", len(scheme.Requires))
	}
	if len(scheme.ParamRefinements) != 1 || len(scheme.ParamRefinements[0].Preds) != 1 || scheme.ParamRefinements[0].Preds[0].Name != "Positive" {
		t.Fatalf("expected extern param refinement to survive fallback, got %+v", scheme.ParamRefinements)
	}
	if len(scheme.ReturnRefinements) != 1 || scheme.ReturnRefinements[0].Name != "Positive" {
		t.Fatalf("expected extern return refinement to survive fallback, got %+v", scheme.ReturnRefinements)
	}
}

func TestCloneSpecSignaturePreservesSplitChannels(t *testing.T) {
	src := `
law Positive(self: i64) = self > 0

def touch(p: mutable i64&) -> i64 ensures p is Positive:
    requires p > 0
    ensure result == 1
    p <- 1
    return 1
`
	result := analyzeTreeTestSource(t, "spec_signature_clone_channels.elisa", src)
	sym, ok := result.GlobalScope.Lookup("touch")
	if !ok {
		t.Fatal("expected touch symbol")
	}
	ft, ok := sym.Type.(*FuncType)
	if !ok || ft.SpecSignature == nil {
		t.Fatalf("expected touch to have a function type with SpecSignature, got %T", sym.Type)
	}
	clone := cloneSpecSignature(ft.SpecSignature)
	if clone == nil {
		t.Fatal("expected cloned signature")
	}
	if len(clone.Requires) != 1 || len(clone.Ensures) != 1 || len(clone.ParamPostconditionLaws) != 1 {
		t.Fatalf("clone lost split channels: requires=%d ensures=%d paramPostconditionLaws=%d", len(clone.Requires), len(clone.Ensures), len(clone.ParamPostconditionLaws))
	}
	clone.Requires = nil
	clone.Ensures = nil
	clone.ParamPostconditionLaws = nil
	if len(ft.SpecSignature.Requires) != 1 || len(ft.SpecSignature.Ensures) != 1 || len(ft.SpecSignature.ParamPostconditionLaws) != 1 {
		t.Fatalf("mutating clone should not mutate original split channels")
	}
}
