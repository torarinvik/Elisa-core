package predicatetypes

import (
	"testing"

	"elisacore/src/ast"
)

func TestReturnHeaderRejectsUnexpectedFeatures(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x", Type: &ast.NamedType{Name: "i64"}}}, ReturnType: &ast.NamedType{Name: "i64"}, Body: []ast.Stmt{&ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}}}
	if _, ok := ReturnHeader(fn, true, 256, 1024); !ok {
		t.Fatal("valid header rejected")
	}
	if _, ok := ReturnHeader(fn, false, 256, 1024); ok {
		t.Fatal("unsupported parameter schema accepted")
	}
	fn.Params[0].Mutable = true
	if _, ok := ReturnHeader(fn, true, 256, 1024); ok {
		t.Fatal("mutable parameter accepted")
	}
}
