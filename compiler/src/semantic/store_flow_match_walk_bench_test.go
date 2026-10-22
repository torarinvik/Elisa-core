package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"testing"
)

func BenchmarkStoreFlowMatchArmWalk(b *testing.B) {
	arms := make([]ast.MatchArm, 128)
	for i := range arms {
		arms[i].Guard = &ast.Ident{Name: "value"}
	}
	root := reflect.ValueOf(&arms).Elem()
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var nested []reflect.Value
		var calls []*ast.CallExpr
		if names := a.storeFlowMentions(root, 0, nil, &nested, &calls, false); len(names) != len(arms) {
			b.Fatal(len(names))
		}
	}
}

func BenchmarkStoreFlowExpressionNames(b *testing.B) {
	var expr ast.Expr = &ast.Ident{Name: "value"}
	for i := 0; i < 64; i++ {
		expr = &ast.CallExpr{Func: &ast.Ident{Name: "unknown"}, Args: []ast.Expr{expr}}
	}
	a := &Analyzer{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(a.storeFlowExprNames(expr)) == 0 {
			b.Fatal("no names")
		}
	}
}

func BenchmarkStoreFlowTypedBinderNames(b *testing.B) {
	idx := &storeFlowBinderIndex{}
	for i := 0; i < 128; i++ {
		idx.typed = append(idx.typed, storeFlowTypedBinder{name: fmt.Sprint("item", i), t: &ast.BuiltinTypeExpr{Name: "i64"}})
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if len(idx.typedNames(storeFlowIntegerTypeExpr)) != 128 {
			b.Fatal("missing names")
		}
	}
}
