package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func refinementCallFixture(extern bool) (*Analyzer, *ast.CallExpr, *FuncType, []ast.ParamDecl, []ast.Expr) {
	params := []ast.ParamDecl{{Name: "x"}, {Name: "y"}, {Name: "z"}, {Name: "w"}}
	requires := []ast.Expr{&ast.BoolLit{Value: true}}
	arg := &ast.Ident{Name: "x"}
	pred := RefinementPredicate{Kind: RefinementPredicateType, Subject: BinderRef{Kind: SpecBinderParam, Position: 0}, LawName: "Positive", Args: []ast.Expr{arg}}
	sig := &SpecSignature{Name: "canonical", Params: []SpecBinder{{Kind: SpecBinderParam, Position: 0, SourceName: "old"}}, ParamPredicates: []RefinementPredicate{pred}, ResultPredicates: []RefinementPredicate{{Subject: BinderRef{Kind: SpecBinderResult}, LawName: "Positive", Args: []ast.Expr{arg}}}, Requires: []RefinementPredicate{{Kind: RefinementPredicateRequires, SourceExpr: &ast.BoolLit{Value: false}}}, Ensures: []RefinementPredicate{{Kind: RefinementPredicateEnsures, SourceExpr: arg}}, ParamPostconditionLaws: []RefinementPredicate{pred}}
	ft := &FuncType{SpecSignature: sig, RefinementEnsures: []RefinementEnsure{{ParamIndex: 1, LawName: "Positive", Args: []ast.Expr{arg}}}}
	var decl ast.Node = &ast.FuncDecl{Name: "callee", Params: params, Requires: requires, IsLemma: true}
	if extern {
		decl = &ast.ExternFuncDecl{Name: "callee", Params: params, Requires: requires}
	}
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: "callee", Kind: SymbolFunc, Node: decl, Type: ft})
	call := &ast.CallExpr{Func: &ast.Ident{Name: "callee"}}
	return &Analyzer{currentScope: scope, exprTypes: map[ast.Expr]Type{call.Func: ft}}, call, ft, params, requires
}

func TestCallRefinementSchemeOwnsResultAndPreservesChannels(t *testing.T) {
	for _, extern := range []bool{false, true} {
		for _, empty := range []bool{false, true} {
			a, call, ft, params, requires := refinementCallFixture(extern)
			if empty {
				params = nil
				requires = nil
				switch d := a.currentScope.Symbols["callee"].Node.(type) {
				case *ast.FuncDecl:
					d.Params = nil
					d.Requires = nil
				case *ast.ExternFuncDecl:
					d.Params = nil
					d.Requires = nil
				}
			}
			expected := cloneRefinementScheme(a.refinementSchemeFromSpecSignature(ft.SpecSignature, ft.RefinementEnsures))
			expected.DeclName = "callee"
			expected.Params = append([]ast.ParamDecl(nil), params...)
			expected.Requires = append([]ast.Expr(nil), requires...)
			expected.IsLemma = !extern
			got, ok := a.callRefinementScheme(call)
			if !ok || !reflect.DeepEqual(expected, got) {
				t.Fatalf("extern=%v empty=%v: scheme differs: %+v", extern, empty, got)
			}
			if len(got.Params) > 0 {
				got.Params[0].Name = "changed"
				got.Requires[0] = nil
			}
			got.ParamRefinements[0].Preds[0].Args[0] = nil
			got.ReturnRefinements[0].Args[0] = nil
			got.ParamRefinementEnsures[0].Args[0] = nil
			got.EnsureValues[0] = nil
			again, _ := a.callRefinementScheme(call)
			if !reflect.DeepEqual(expected, again) {
				t.Fatalf("mutating call scheme contaminated next call: %+v", again)
			}
		}
	}
}

func BenchmarkCallRefinementScheme(b *testing.B) {
	a, call, _, _, _ := refinementCallFixture(false)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := a.callRefinementScheme(call); !ok {
			b.Fatal("missing scheme")
		}
	}
}

// Reference copy used by the pre-optimization call adapter.
func cloneRefinementScheme(s RefinementScheme) RefinementScheme {
	out := s
	out.Params = append([]ast.ParamDecl(nil), s.Params...)
	out.Requires = append([]ast.Expr(nil), s.Requires...)
	out.EnsureValues = append([]ast.Expr(nil), s.EnsureValues...)
	out.ParamRefinements = append([]ParamRefinementScheme(nil), s.ParamRefinements...)
	for i := range out.ParamRefinements {
		out.ParamRefinements[i].Preds = append([]ast.RefinementPredExpr(nil), s.ParamRefinements[i].Preds...)
	}
	out.ReturnRefinements = append([]ast.RefinementPredExpr(nil), s.ReturnRefinements...)
	out.ParamRefinementEnsures = append([]ParamRefinementEnsureScheme(nil), s.ParamRefinementEnsures...)
	for i := range out.ParamRefinementEnsures {
		out.ParamRefinementEnsures[i].Args = append([]ast.Expr(nil), s.ParamRefinementEnsures[i].Args...)
	}
	return out
}
