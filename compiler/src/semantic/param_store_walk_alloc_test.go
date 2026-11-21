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
		node     any
		children []string
	}{
		{ast.ExprStmt{}, []string{"Expr"}},
		{ast.FieldExpr{}, []string{"Object"}},
		{ast.BinaryExpr{}, []string{"Left", "Right", "LoweredCall"}},
		{ast.UnaryExpr{}, []string{"Operand", "LoweredCall"}},
		{ast.ReturnStmt{}, []string{"Value"}},
		{ast.VarDeclStmt{}, []string{"Type", "Value", "Owner"}},
		{ast.IfStmt{}, []string{"Cond", "Then", "Elifs", "Else"}},
		{ast.ElifClause{}, []string{"Cond", "Body"}},
		{ast.WhileStmt{}, []string{"Cond", "Body"}},
		{ast.AssignStmt{}, []string{"Target", "Value", "AsOverlayCall"}},
	} {
		typ := reflect.TypeOf(item.node)
		fields := syntaxWalkFields(typ)
		names := make([]string, len(fields))
		for index, field := range fields {
			names[index] = typ.Field(field).Name
		}
		if !reflect.DeepEqual(names, item.children) {
			t.Errorf("%s syntax children changed; update the parameter store walk", typ)
		}
	}
}
