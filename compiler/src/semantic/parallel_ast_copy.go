package semantic

import (
	"reflect"
	"sync"

	"elisacore/src/ast"
)

var astGraphPkgPath = reflect.TypeOf(ast.File{}).PkgPath()

// copyASTGraph deep-copies an AST value: pointers to ast-package types, slices,
// maps and interfaces holding them are copied, each pointer once (internal
// sharing is kept); everything else, such as positions and strings, is shared.
// It backs declarations whose aliased slice storage the generated syntax
// snapshot declines.
func copyASTGraph(v reflect.Value, memo map[uintptr]reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || v.Type().Elem().PkgPath() != astGraphPkgPath {
			return v
		}
		if done, ok := memo[v.Pointer()]; ok {
			return done
		}
		out := reflect.New(v.Type().Elem())
		memo[v.Pointer()] = out
		copyASTGraphInto(out.Elem(), v.Elem(), memo)
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := copyASTGraph(v.Elem(), memo)
		out := reflect.New(v.Type()).Elem()
		out.Set(inner)
		return out
	case reflect.Struct:
		if v.Type().PkgPath() != astGraphPkgPath {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		copyASTGraphInto(out, v, memo)
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		if astCopyNeedsTraversal(v.Type().Elem()) {
			for i := 0; i < v.Len(); i++ {
				copyASTGraphInto(out.Index(i), v.Index(i), memo)
			}
		} else {
			reflect.Copy(out, v)
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(copyASTGraph(iter.Key(), memo), copyASTGraph(iter.Value(), memo))
		}
		return out
	}
	return v
}

// Copy into an existing field or slice element. AST pointer allocation already
// provides its struct storage, and interface fields can hold a copied node
// directly; neither needs an intermediate boxed value.
func copyASTGraphInto(out, source reflect.Value, memo map[uintptr]reflect.Value) {
	switch source.Kind() {
	case reflect.Struct:
		if source.Type().PkgPath() == astGraphPkgPath {
			out.Set(source)
			for _, i := range astCopyTraversalFields(source.Type()) {
				copyASTGraphInto(settable(out.Field(i)), settable(source.Field(i)), memo)
			}
			return
		}
	case reflect.Interface:
		if !source.IsNil() {
			out.Set(copyASTGraph(source.Elem(), memo))
			return
		}
	}
	out.Set(copyASTGraph(source, memo))
}

var astCopyFieldCache sync.Map // reflect.Type -> immutable []int

func astCopyNeedsTraversal(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Slice, reflect.Map:
		return true
	case reflect.Pointer:
		return t.Elem().PkgPath() == astGraphPkgPath
	case reflect.Struct:
		return t.PkgPath() == astGraphPkgPath
	default:
		return false
	}
}

func astCopyTraversalFields(t reflect.Type) []int {
	if fields, ok := astCopyFieldCache.Load(t); ok {
		return fields.([]int)
	}
	var fields []int
	for i := 0; i < t.NumField(); i++ {
		if astCopyNeedsTraversal(t.Field(i).Type) {
			fields = append(fields, i)
		}
	}
	actual, _ := astCopyFieldCache.LoadOrStore(t, fields)
	return actual.([]int)
}

func copyASTDecl(d ast.Decl) ast.Decl {
	return copyASTGraph(reflect.ValueOf(&d).Elem(), map[uintptr]reflect.Value{}).Interface().(ast.Decl)
}
