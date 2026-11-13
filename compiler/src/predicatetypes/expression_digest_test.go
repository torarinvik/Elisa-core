package predicatetypes

import (
	"testing"

	"elisacore/src/ast"
)

func TestExpressionDigestProtocol(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f", Params: []ast.ParamDecl{{Name: "x", Type: &ast.NamedType{Name: "i64"}}}, ReturnType: &ast.NamedType{Name: "i64"}}
	got := ExpressionDigest(fn, []ast.Expr{&ast.Ident{Name: "x"}})
	const want = "c1275b73c88c43b022e077798908538181a38895ec450ed0835ed09c32ce491c"
	if got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}
