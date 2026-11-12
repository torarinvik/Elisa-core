package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestForwardedRegionPathsAndAnnotations(t *testing.T) {
	root := &ast.Ident{Name: "param"}
	path := &ast.AddrOfExpr{Operand: &ast.IndexExpr{Object: &ast.FieldExpr{Object: root, Field: "items"}}}
	name, field, ok := RegionForwardedParamPath(path)
	if !ok || !field || name != "param" {
		t.Fatalf("name=%q field=%v ok=%v", name, field, ok)
	}
	if !ForwardsParamIdent(&ast.AddrOfExpr{Operand: root}, "param") || ForwardsParamIdent(path, "param") {
		t.Fatal("direct forwarding classification changed")
	}
	typ := &ast.MutableType{Elem: &ast.RefType{Elem: &ast.BuiltinTypeExpr{Name: "darray", Region: "r"}}}
	if !TypeExprCarriesRegionParam(typ, map[string]bool{"r": true}) || TypeExprCarriesRegionParam(typ, map[string]bool{"other": true}) {
		t.Fatal("region parameter classification changed")
	}
}
