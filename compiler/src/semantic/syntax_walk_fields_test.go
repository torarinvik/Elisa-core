package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// Compare callback order and pointer identity, including lowered links and a
// future-shaped nested struct. Callers inspect pointers/composites, not scalar
// fields; pruning must preserve every reachable callback in that contract.
func TestSyntaxWalkFieldsPreservesCompositeCallbacks(t *testing.T) {
	call := &ast.CallExpr{Func: &ast.Ident{Name: "callee"}, Args: []ast.Expr{&ast.IntLit{Value: "9"}}}
	expr := &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: &ast.IntLit{Value: "1"}, Right: &ast.UnaryExpr{Op: lexer.TOKEN_MINUS, Operand: &ast.IntLit{Value: "2"}, LoweredCall: call}, LoweredCall: call}
	scalar := 3
	fixture := struct {
		Position lexer.Pos
		Name     string
		Body     []ast.Stmt
		Future   struct {
			Inner         ast.Expr
			ScalarPointer *int
		}
	}{Position: lexer.Pos{File: "fixture.elisa"}, Name: "metadata", Body: []ast.Stmt{&ast.ReturnStmt{Value: expr}}}
	fixture.Future.Inner = call
	fixture.Future.ScalarPointer = &scalar
	collect := func(planned bool) []any {
		var result []any
		var walk func(reflect.Value)
		walk = func(v reflect.Value) {
			if !v.IsValid() || !v.CanInterface() {
				return
			}
			switch v.Kind() {
			case reflect.Pointer:
				if v.IsNil() {
					return
				}
				result = append(result, v.Interface())
				walk(v.Elem())
			case reflect.Interface:
				if !v.IsNil() {
					walk(v.Elem())
				}
			case reflect.Struct:
				if planned {
					for _, i := range syntaxWalkFields(v.Type()) {
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
		return result
	}
	before, after := collect(false), collect(true)
	if len(before) == 0 || len(before) != len(after) {
		t.Fatalf("lost callbacks: %d vs %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("callback %d changed: %T vs %T", i, before[i], after[i])
		}
	}
}

func BenchmarkSyntaxWalkCompositeFields(b *testing.B) {
	var expression func(int) ast.Expr
	expression = func(depth int) ast.Expr {
		if depth == 0 {
			return &ast.IntLit{Value: "1"}
		}
		return &ast.BinaryExpr{Op: lexer.TOKEN_PLUS, Left: expression(depth - 1), Right: expression(depth - 1)}
	}
	body := []ast.Stmt{&ast.ReturnStmt{Value: expression(9)}}
	for _, planned := range []bool{false, true} {
		name := "all-fields"
		if planned {
			name = "composite-fields"
		}
		b.Run(name, func(b *testing.B) {
			var walk func(reflect.Value) int
			walk = func(v reflect.Value) int {
				if !v.IsValid() || !v.CanInterface() {
					return 0
				}
				switch v.Kind() {
				case reflect.Pointer:
					if v.IsNil() {
						return 0
					}
					return 1 + walk(v.Elem())
				case reflect.Interface:
					if !v.IsNil() {
						return walk(v.Elem())
					}
				case reflect.Struct:
					count := 0
					if planned {
						for _, i := range syntaxWalkFields(v.Type()) {
							count += walk(v.Field(i))
						}
					} else {
						for i := 0; i < v.NumField(); i++ {
							count += walk(v.Field(i))
						}
					}
					return count
				case reflect.Slice, reflect.Array:
					count := 0
					for i := 0; i < v.Len(); i++ {
						count += walk(v.Index(i))
					}
					return count
				}
				return 0
			}
			b.ReportAllocs()
			for range b.N {
				if count := walk(reflect.ValueOf(body)); count != 1024 {
					b.Fatalf("visited %d nodes", count)
				}
			}
		})
	}
}
