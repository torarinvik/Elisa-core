//go:build cgo

package semantic

import (
	"elisacore/src/ast"
	"testing"
)

func TestCallDisjointScansOnlyWhenContainerPairIsPossible(t *testing.T) {
	scalar := &BuiltinType{Name: "bool"}
	container := &RefType{Elem: &DArrayType{Elem: scalar}}
	for _, tc := range []struct {
		name   string
		params []Type
		args   int
		scan   bool
	}{
		{"scalar", []Type{scalar}, 1, false},
		{"one_container", []Type{container, scalar}, 2, false},
		{"missing_second_argument", []Type{container, container}, 1, false},
		{"container_pair", []Type{container, container}, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			analyzer := &Analyzer{currentFuncDecl: &ast.FuncDecl{}, callArgDisjoint: map[*ast.CallExpr]*CallArgDisjointInfo{}}
			args := make([]ast.Expr, tc.args)
			for i := range args {
				args[i] = &ast.BoolLit{Value: true}
			}
			analyzer.recordCallArgDisjoint(&ast.CallExpr{}, tc.params, args)
			if (analyzer.privateFreshDArrayCache != nil) != tc.scan {
				t.Fatalf("provenance scan=%v, want %v", analyzer.privateFreshDArrayCache != nil, tc.scan)
			}
		})
	}
}
