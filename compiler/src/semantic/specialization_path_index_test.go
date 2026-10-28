package semantic

import (
	"reflect"
	"sync"
	"testing"

	"elisacore/src/ast"
)

func TestSpecializationASTPathIndexMatchesSchema(t *testing.T) {
	types := []reflect.Type{
		reflect.TypeFor[*ast.FuncDecl](), reflect.TypeFor[*ast.CallExpr](),
		reflect.TypeFor[*ast.Ident](), reflect.TypeFor[*ast.ReturnStmt](),
		reflect.TypeFor[*ast.IndexExpr](), reflect.TypeFor[*ast.VarDeclStmt](),
	}
	analyzer := reflect.TypeFor[Analyzer]()
	for _, path := range specializationASTMapPaths() {
		typ := analyzer
		for _, i := range path {
			typ = typ.Field(i).Type
		}
		types = append(types, typ.Key())
	}
	for _, nodeType := range types {
		var want [][]int
		for _, path := range specializationASTMapPaths() {
			typ := analyzer
			for _, i := range path {
				typ = typ.Field(i).Type
			}
			if nodeType.AssignableTo(typ.Key()) {
				want = append(want, path)
			}
		}
		if got := specializationASTMapPathsForNode(nodeType); !reflect.DeepEqual(got, want) {
			t.Fatalf("%v: indexed paths differ from exact schema scan: got %v, want %v", nodeType, got, want)
		}
	}
}

// Concurrent specializations share only the immutable schema index.
func TestSpecializationASTPathIndexConcurrent(t *testing.T) {
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for n := 0; n < 100; n++ {
				if len(specializationASTMapPathsForNode(reflect.TypeFor[*ast.FuncDecl]())) == 0 {
					t.Error("missing function paths")
				}
			}
		}()
	}
	workers.Wait()
}
