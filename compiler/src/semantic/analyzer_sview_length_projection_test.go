package semantic

import (
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

func TestImmutableSViewLengthProjectionDistinguishesShadowedBindings(t *testing.T) {
	analyzer := &Analyzer{}
	outer := NewScope(nil)
	fn := &ast.FuncDecl{Position: lexer.Pos{File: "shadow.elisa", Line: 1, Offset: 1}}
	outer.Define(&Symbol{
		Name: "text", Kind: SymbolParam, Type: &SViewType{}, Node: fn, ParamIndex: 0,
	})
	inner := NewScope(outer)
	local := &ast.VarDeclStmt{Position: lexer.Pos{File: "shadow.elisa", Line: 3, Offset: 40}}
	inner.Define(&Symbol{Name: "text", Kind: SymbolLocal, Type: &SViewType{}, Node: local})

	projection := &ast.FieldExpr{Object: &ast.Ident{Name: "text"}, Field: "len"}
	outerKey, outerOK := immutableSViewLengthProjection(analyzer, outer, projection)
	innerKey, innerOK := immutableSViewLengthProjection(analyzer, inner, projection)
	if !outerOK || !innerOK {
		t.Fatalf("expected both immutable sview bindings to have stable projections, outer=%t inner=%t", outerOK, innerOK)
	}
	if outerKey == innerKey {
		t.Fatalf("shadowed sview bindings must have distinct projection keys, got %q", outerKey)
	}
}
