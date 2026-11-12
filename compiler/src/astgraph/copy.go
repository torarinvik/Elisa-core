package astgraph

import (
	"reflect"
	"sync"
	"unsafe"

	"elisacore/src/ast"
)

var PackagePath = reflect.TypeOf(ast.File{}).PkgPath()

// Copy deep-copies an AST value: pointers to ast-package types, slices,
// maps and interfaces holding them are copied, each pointer once (internal
// sharing is kept); everything else, such as positions and strings, is shared.
// It backs declarations whose aliased slice storage the generated syntax
// snapshot declines.
func Copy(v reflect.Value, memo map[uintptr]reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || v.Type().Elem().PkgPath() != PackagePath {
			return v
		}
		if done, ok := memo[v.Pointer()]; ok {
			return done
		}
		out := reflect.New(v.Type().Elem())
		memo[v.Pointer()] = out
		copyGraphInto(out.Elem(), v.Elem(), memo)
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		inner := Copy(v.Elem(), memo)
		out := reflect.New(v.Type()).Elem()
		out.Set(inner)
		return out
	case reflect.Struct:
		if v.Type().PkgPath() != PackagePath {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		copyGraphInto(out, v, memo)
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		if NeedsTraversal(v.Type().Elem()) {
			for i := 0; i < v.Len(); i++ {
				copyGraphInto(out.Index(i), v.Index(i), memo)
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
			out.SetMapIndex(Copy(iter.Key(), memo), Copy(iter.Value(), memo))
		}
		return out
	}
	return v
}

// Copy into an existing field or slice element. AST pointer allocation already
// provides its struct storage, and interface fields can hold a copied node
// directly; neither needs an intermediate boxed value.
func copyGraphInto(out, source reflect.Value, memo map[uintptr]reflect.Value) {
	switch source.Kind() {
	case reflect.Struct:
		if source.Type().PkgPath() == PackagePath {
			out.Set(source)
			for _, i := range TraversalFields(source.Type()) {
				copyGraphInto(settable(out.Field(i)), settable(source.Field(i)), memo)
			}
			return
		}
	case reflect.Interface:
		if !source.IsNil() {
			out.Set(Copy(source.Elem(), memo))
			return
		}
	}
	out.Set(Copy(source, memo))
}

var astCopyFieldCache sync.Map // reflect.Type -> immutable []int

func NeedsTraversal(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Slice, reflect.Map:
		return true
	case reflect.Pointer:
		return t.Elem().PkgPath() == PackagePath
	case reflect.Struct:
		return t.PkgPath() == PackagePath
	default:
		return false
	}
}

func TraversalFields(t reflect.Type) []int {
	if fields, ok := astCopyFieldCache.Load(t); ok {
		return fields.([]int)
	}
	var fields []int
	for i := 0; i < t.NumField(); i++ {
		if NeedsTraversal(t.Field(i).Type) {
			fields = append(fields, i)
		}
	}
	actual, _ := astCopyFieldCache.LoadOrStore(t, fields)
	return actual.([]int)
}

func CopyDecl(d ast.Decl) ast.Decl {
	return Copy(reflect.ValueOf(&d).Elem(), map[uintptr]reflect.Value{}).Interface().(ast.Decl)
}

// settable preserves access to addressable reflected fields while copying ASTs.
func settable(v reflect.Value) reflect.Value {
	if v.CanSet() || !v.CanAddr() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}
