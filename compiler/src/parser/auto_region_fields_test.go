package parser

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
	"reflect"
	"sync"
	"testing"
)

// Compare every observable pointer with the old unfiltered reflection traversal.
// This catches missed nodes in nested structs, arrays, slices and interfaces,
// while preserving the existing exclusion of maps and unexported fields.
func TestAutoRegionFieldPlansPreserveReachableNodes(t *testing.T) {
	type nested struct {
		Position lexer.Pos
		Values   [2]ast.Expr
		Names    []string
	}
	fixture := struct {
		Position  lexer.Pos
		Nodes     []nested
		Body      []ast.Stmt
		Pointer   *ast.IntLit
		Nil       *ast.ExprBlock
		HiddenMap map[string]ast.Expr
		hidden    ast.Expr
	}{
		Nodes:     []nested{{Values: [2]ast.Expr{&ast.AllocExpr{AutoRegion: true}, &ast.ExprBlock{Value: &ast.IntLit{Value: "1"}}}, Names: []string{"scalar", "leaf"}}},
		Body:      []ast.Stmt{&ast.VarDeclStmt{Name: "value", Value: &ast.StructLitExpr{}}, &ast.ReturnStmt{Value: &ast.Ident{Name: "value"}}},
		Pointer:   &ast.IntLit{Value: "2"},
		HiddenMap: map[string]ast.Expr{"ignored": &ast.AllocExpr{}}, hidden: &ast.AllocExpr{},
	}
	collect := func(filtered bool) map[uintptr]reflect.Type {
		found := map[uintptr]reflect.Type{}
		var walk func(reflect.Value)
		walk = func(v reflect.Value) {
			if !v.IsValid() || !v.CanInterface() {
				return
			}
			switch v.Kind() {
			case reflect.Pointer:
				if !v.IsNil() {
					found[v.Pointer()] = v.Type()
					walk(v.Elem())
				}
			case reflect.Interface:
				if !v.IsNil() {
					walk(v.Elem())
				}
			case reflect.Struct:
				if filtered {
					for _, i := range autoRegionChildFields(v.Type()) {
						walk(v.Field(i))
					}
				} else {
					for i := 0; i < v.NumField(); i++ {
						walk(v.Field(i))
					}
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					walk(v.Index(i))
				}
			}
		}
		walk(reflect.ValueOf(fixture))
		return found
	}
	expected := collect(false)
	if len(expected) < 8 {
		t.Fatal("fixture failed to cover nested nodes")
	}
	if actual := collect(true); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("filtered traversal lost nodes: %v / %v", actual, expected)
	}
}

func TestAutoRegionFieldPlansConcurrentPublication(t *testing.T) {
	type coldPlan struct {
		Pos    lexer.Pos
		Values [2][]ast.Expr
		Names  []string
	}
	typ := reflect.TypeOf(coldPlan{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 64; j++ {
				fields := autoRegionChildFields(typ)
				if !reflect.DeepEqual(fields, []int{1}) {
					t.Errorf("incomplete published field plan: %v", fields)
					return
				}
			}
		}()
	}
	wg.Wait()
}
