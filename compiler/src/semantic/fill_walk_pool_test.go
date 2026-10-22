package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

func TestFillWalkNestedAndReusedSets(t *testing.T) {
	for i := 0; i < 8; i++ {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			t.Parallel()
			statement := &ast.ExprStmt{Expr: &ast.IntLit{Value: "1"}}
			root := reflect.ValueOf([]ast.Stmt{statement, statement})
			var expected []any
			fillMayAdoptWalkReflect(root, func(node any) { expected = append(expected, node) }, map[uintptr]bool{})
			for iteration := 0; iteration < 10; iteration++ {
				var outer, inner []any
				fillMayAdoptWalk(root, func(node any) {
					outer = append(outer, node)
					if len(outer) == 1 {
						fillMayAdoptWalk(root, func(node any) { inner = append(inner, node) })
					}
				})
				if !reflect.DeepEqual(outer, expected) || !reflect.DeepEqual(inner, expected) {
					t.Fatal("nested or repeated walk changed visit order or shared-node deduplication")
				}
			}
		})
	}
}
