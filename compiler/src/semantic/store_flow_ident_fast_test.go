package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"testing"
)

func TestStoreFlowIdentFastMatchesReflectiveWalk(t *testing.T) {
	saved := storeFlowIdentFast
	defer func() { storeFlowIdentFast = saved }()
	if !saved {
		t.Skip("identifier schema changed; reflective walk is active")
	}
	id := func(file, name string) *ast.Ident {
		return &ast.Ident{Position: lexer.Pos{File: file, Line: 8}, Name: name}
	}
	exprs := []ast.Expr{
		id("source", "name"), id("file.elisa", "Upper"), id("\xff", "Äther"),
		&ast.CallExpr{Func: id("", "call"), Args: []ast.Expr{id("source", "name")}},
		&ast.QueryExpr{Kind: ast.QueryExprAny, Name: "item", Source: id("source", "name"), Filter: id("", "predicate")},
		&ast.TernaryExpr{Value: id("source", "name"), Cond: id("", "condition"), Alt: id("", "other")},
	}
	for _, expr := range exprs {
		for _, callsOnly := range []bool{false, true} {
			for _, collectNames := range []bool{false, true} {
				run := func(fast bool) ([]string, [][]string, bool) {
					storeFlowIdentFast = fast
					a := &Analyzer{storeFlowEnv: map[string]string{"source": "renamed_source", "name": "renamed_name"}}
					q := storeFlowQueryScan{}
					names := a.storeFlowCollectMentionsWithQuery(reflect.ValueOf(expr), 0, nil, nil, nil, callsOnly, collectNames, &q)
					return names, q.pairs, q.incomplete
				}
				oldNames, oldPairs, oldIncomplete := run(false)
				newNames, newPairs, newIncomplete := run(true)
				if !reflect.DeepEqual(oldNames, newNames) || !reflect.DeepEqual(oldPairs, newPairs) || oldIncomplete != newIncomplete {
					t.Fatalf("%T callsOnly=%v collectNames=%v: old %v %v %v, new %v %v %v", expr, callsOnly, collectNames, oldNames, oldPairs, oldIncomplete, newNames, newPairs, newIncomplete)
				}
			}
		}
	}
}
