package progresscalls

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func call(name string) ast.Expr { return &ast.CallExpr{Func: &ast.Ident{Name: name}} }

func TestCollectDirectCallsInDictionaryAndSecondIndex(t *testing.T) {
	stmts := []ast.Stmt{
		&ast.ExprStmt{Expr: &ast.ListLitExpr{Brace: true, Keys: []ast.Expr{call("key")}, Elems: []ast.Expr{call("value")}}},
		&ast.ExprStmt{Expr: &ast.IndexExpr{Object: call("object"), Index: call("first"), Index2: call("second"), Fallback: call("fallback")}},
		&ast.ExprStmt{Expr: &ast.ListLitExpr{Elems: []ast.Expr{call("array")}}},
		&ast.ExprStmt{Expr: &ast.ListLitExpr{Keys: []ast.Expr{call("orphan")}}},
	}
	want := []string{"key", "value", "object", "first", "second", "fallback", "array", "orphan"}
	if got := CollectDirectCalls(stmts); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
