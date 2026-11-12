package parserdesugar

import (
	"elisacore/src/ast"
	"testing"
)

func TestDStrStringLiteralInit(t *testing.T) {
	text := &ast.StringLit{Value: "Aé"}
	result, ok := DStrStringLiteralInit(&ast.NamedType{Name: "dstr"}, text).(*ast.ListLitExpr)
	if !ok || len(result.Elems) != 3 {
		t.Fatal("expected UTF-8 bytes")
	}
	regionType := &ast.NamedType{Name: "dstr", Region: "r"}
	if _, ok := DStrStringLiteralInit(regionType, text).(*ast.ListLitExpr); !ok {
		t.Fatal("region-annotated dstr must retain its literal desugaring")
	}
	if DStrStringLiteralInit(&ast.NamedType{Name: "cstr"}, text) != text {
		t.Fatal("rewrote non-dstr")
	}
}
