package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

func TestStoreFlowEmptyScopeClearsStatementRename(t *testing.T) {
	inner := &ast.ReturnStmt{Value: &ast.Ident{Name: "x"}}
	root := &ast.IfStmt{Then: []ast.Stmt{inner}}
	a := &Analyzer{storeFlowStmtEnv: map[uintptr]map[string]string{}, storeFlowEnv: map[string]string{"x": "x#scope"}}
	key := reflect.ValueOf(inner).Pointer()
	a.storeFlowCollectMentionsWithQuery(reflect.ValueOf(root).Elem(), reflect.ValueOf(root).Pointer(), nil, nil, nil, false, false, nil)
	if a.storeFlowStmtEnv[key]["x"] != "x#scope" {
		t.Fatal("missing renamed scope")
	}
	a.storeFlowEnv = nil
	a.storeFlowCollectMentionsWithQuery(reflect.ValueOf(root).Elem(), reflect.ValueOf(root).Pointer(), nil, nil, nil, false, false, nil)
	if _, ok := a.storeFlowStmtEnv[key]; ok {
		t.Fatal("empty scope retained a stale rename")
	}
}
