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
		&ast.BinaryExpr{Position: lexer.Pos{File: "source", Line: 12, Col: 8}, Left: id("", "name"), Right: &ast.IntLit{Position: lexer.Pos{File: "literal"}, Value: "1"}},
		&ast.CallExpr{Position: lexer.Pos{File: "source"}, Func: id("", "call"), Args: []ast.Expr{&ast.IntLit{Position: lexer.Pos{File: "literal"}, Value: "1"}}},
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

func TestStoreFlowSimpleExprNamesMatchesWalk(t *testing.T) {
	if !storeFlowIdentFast {
		t.Skip("identifier schema changed")
	}
	old := storeFlowIdentFast
	defer func() { storeFlowIdentFast = old }()
	for _, id := range []*ast.Ident{{Position: lexer.Pos{File: "source"}, Name: "value"}, {Position: lexer.Pos{File: "file.elisa"}, Name: "Upper"}, {Name: "source"}} {
		a := &Analyzer{storeFlowEnv: map[string]string{"source": "renamed"}}
		storeFlowIdentFast = false
		want := a.storeFlowArgNames(id, nil)
		storeFlowIdentFast = true
		a.storeFlowBinds = true
		got := a.storeFlowArgNames(id, nil)
		if !reflect.DeepEqual(got, want) || a.storeFlowBinds {
			t.Fatalf("got %v binds %v, want %v", got, a.storeFlowBinds, want)
		}
	}
}

func BenchmarkStoreFlowSimpleArgument(b *testing.B) {
	id := &ast.Ident{Name: "value"}
	for _, fast := range []bool{false, true} {
		label := "reflective"
		if fast {
			label = "direct"
		}
		b.Run(label, func(b *testing.B) {
			old := storeFlowIdentFast
			storeFlowIdentFast = fast
			defer func() { storeFlowIdentFast = old }()
			a := &Analyzer{}
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = a.storeFlowArgNames(id, nil)
			}
		})
	}
}
