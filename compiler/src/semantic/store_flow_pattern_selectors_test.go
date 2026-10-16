package semantic

import (
	"elisacore/src/ast"
	"strconv"
	"testing"
)

func TestStoreFlowPatternSelectorsAreNotBindings(t *testing.T) {
	pattern := &ast.MatchVariantPattern{EnumName: "Expr", Variant: "Group", As: "whole", Args: []ast.MatchPatternArg{
		{Name: "Expr", Pattern: &ast.MatchBindPattern{Name: "payload"}},
	}}
	decl := &ast.FuncDecl{Body: []ast.Stmt{&ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: pattern}}}}}
	names := buildStoreFlowBinderIndex(decl).binderNames()
	for _, name := range []string{"whole", "payload"} {
		if !names[name] {
			t.Errorf("missing actual binding %q", name)
		}
	}
	for _, name := range []string{"Expr", "Group"} {
		if names[name] {
			t.Errorf("selector/field label %q is not a binding", name)
		}
	}
	variant := &EnumVariant{Name: "Ident", Payload: []Type{&SViewType{}}}
	enum := &EnumType{Name: "Expr", Variants: []*EnumVariant{variant}, VariantMap: map[string]*EnumVariant{"Ident": variant}}
	a := &Analyzer{namedTypes: map[string]Type{"Expr": enum}, storeFlowCurrentDecl: decl}
	call := &ast.CallExpr{Func: &ast.FieldExpr{Object: &ast.Ident{Name: "Expr"}, Field: "Ident"}, Args: []ast.Expr{&ast.Ident{Name: "name"}}}
	if !a.storeFlowEnumVariantConstructor(call) {
		t.Fatal("matching another variant must not shadow its enum constructor")
	}
	// Replacing metadata with an actual value binding must still preserve shadowing.
	decl = &ast.FuncDecl{Body: []ast.Stmt{&ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: &ast.MatchBindPattern{Name: "Expr"}}}}}}
	a.storeFlowCurrentDecl = decl
	if a.storeFlowEnumVariantConstructor(call) {
		t.Fatal("a real pattern value binding must shadow the enum")
	}
}

func BenchmarkStoreFlowPatternBinderIndex(b *testing.B) {
	decl := &ast.FuncDecl{}
	for i := 0; i < 1024; i++ {
		pattern := &ast.MatchVariantPattern{EnumName: "Expr", Variant: "Group", As: "whole", Args: []ast.MatchPatternArg{
			{Name: "field", Pattern: &ast.MatchBindPattern{Name: "payload" + strconv.Itoa(i)}},
		}}
		decl.Body = append(decl.Body, &ast.MatchStmt{Arms: []ast.MatchArm{{Pattern: pattern}}})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildStoreFlowBinderIndex(decl).binderNames()
	}
}
