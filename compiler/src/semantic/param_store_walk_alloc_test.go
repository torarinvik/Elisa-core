package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func BenchmarkWalkParamStoresScopes(b *testing.B) {
	body := []ast.Stmt{&ast.ReturnStmt{}}
	for i := 0; i < 128; i++ {
		body = []ast.Stmt{&ast.IfStmt{Cond: &ast.BoolLit{Value: true}, Then: body}}
	}
	a := &Analyzer{}
	add := func(string, string) {}
	global := func(string) {}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.walkParamStores(body, nil, nil, add, global, nil)
	}
}

func TestParamStoreWalkFastPathsCoverSyntaxChildren(t *testing.T) {
	for _, item := range []struct {
		node  any
		child string
	}{
		{ast.ExprStmt{}, "Expr"},
		{ast.FieldExpr{}, "Object"},
	} {
		typ := reflect.TypeOf(item.node)
		fields := syntaxWalkFields(typ)
		if len(fields) != 1 || typ.Field(fields[0]).Name != item.child {
			t.Errorf("%s syntax children changed; update the parameter store walk", typ)
		}
	}
}
