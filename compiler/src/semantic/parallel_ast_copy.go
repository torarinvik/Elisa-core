package semantic

import (
	"reflect"

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
		out.Elem().Set(copyASTGraph(v.Elem(), memo))
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
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			f := settable(out.Field(i))
			f.Set(copyASTGraph(settable(v.Field(i)), memo))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(copyASTGraph(v.Index(i), memo))
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

func copyASTDecl(d ast.Decl) ast.Decl {
	return copyASTGraph(reflect.ValueOf(&d).Elem(), map[uintptr]reflect.Value{}).Interface().(ast.Decl)
}
