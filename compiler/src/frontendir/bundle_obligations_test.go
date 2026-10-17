package frontendir

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func TestBundleRoundTripsExternLawObligations(t *testing.T) {
	decl := &ast.ExternFuncDecl{Name: "work", Fulfills: []ast.FulfillsClause{
		{Position: lexer.Pos{File: "work.elisai", Line: 3, Col: 4}, Law: "Leaf"},
		{Position: lexer.Pos{File: "work.elisai", Line: 3, Col: 10}, Param: "s", Law: "PixelOnly"},
	}}
	data, err := Encode(&Bundle{File: &ast.File{Decls: []ast.Decl{decl}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.File.Decls) != 1 || !reflect.DeepEqual(decl, got.File.Decls[0]) {
		t.Fatalf("extern law obligations lost: %#v", got.File.Decls)
	}
}
