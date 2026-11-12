package regionframesyntax

import (
	"elisacore/src/ast"
	"testing"
)

func TestFactoryTypeClassificationPreservesRegionRules(t *testing.T) {
	fn := &ast.FuncDecl{Params: []ast.ParamDecl{{Type: &ast.MutableType{Elem: &ast.RefType{Elem: &ast.NamedType{Name: "Arena"}}}}}}
	if !HasArenaParam(fn) {
		t.Fatal("wrapped arena parameter was missed")
	}
	if !ReturnTypeIsOwnedContainer(&ast.BuiltinTypeExpr{Name: "darray"}) {
		t.Fatal("regionless darray should qualify")
	}
	if ReturnTypeIsOwnedContainer(&ast.BuiltinTypeExpr{Name: "darray", Region: "caller"}) {
		t.Fatal("explicit region must not qualify")
	}
	if ReturnTypeIsOwnedContainer(&ast.NamedType{Name: "sview"}) {
		t.Fatal("borrowed view must not qualify")
	}
	if TypeExprIsRegionless(&ast.BuiltinTypeExpr{Name: "darray", Region: "caller"}) || !TypeExprIsRegionless(&ast.NamedType{Name: "dstr"}) {
		t.Fatal("region annotation classification changed")
	}
}
