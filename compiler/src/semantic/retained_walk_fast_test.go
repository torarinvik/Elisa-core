package semantic

import (
	"reflect"
	"testing"

	"elisacore/src/ast"
)

// Keep the original reflective walk as an independent ordering and nil oracle.
func retainedWalkReference(file *ast.File) []ast.Decl {
	var out []ast.Decl
	var visit func(reflect.Value)
	visit = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Pointer && v.Type().Implements(retainedDeclType) {
				d := v.Interface().(ast.Decl)
				out = append(out, d)
				if _, ok := d.(*ast.FuncDecl); ok {
					return
				}
			}
			visit(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if f := v.Field(i); f.Kind() == reflect.Slice || f.Kind() == reflect.Interface || f.Kind() == reflect.Pointer {
					visit(f)
				}
			}
		case reflect.Slice:
			if v.Type().Elem().Kind() != reflect.Uint8 {
				for i := 0; i < v.Len(); i++ {
					visit(v.Index(i))
				}
			}
		}
	}
	if file != nil {
		for i := range file.Decls {
			visit(reflect.ValueOf(&file.Decls[i]).Elem())
		}
	}
	return out
}

func TestRetainedWalkFastMatchesReference(t *testing.T) {
	var nilFn *ast.FuncDecl
	file := &ast.File{Decls: []ast.Decl{
		nil, nilFn, &ast.FuncDecl{Name: "outer"},
		&ast.NamespaceDecl{Name: "module", Decls: []ast.Decl{
			nil, nilFn, &ast.FuncDecl{Name: "inner"},
			&ast.StaticIfDecl{Then: []ast.Decl{&ast.FuncDecl{Name: "then"}},
				Elifs: []ast.StaticElifDecl{{Body: []ast.Decl{&ast.FuncDecl{Name: "elif"}}}},
				Else:  []ast.Decl{&ast.FuncDecl{Name: "else"}}},
		}},
	}}
	for _, input := range []*ast.File{nil, {}, file} {
		if got, want := retainedWalkDecls(input), retainedWalkReference(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("declaration walk changed: got %#v, want %#v", got, want)
		}
	}
}
