package semantic

import (
	"reflect"
	"sync"

	"elisacore/src/ast"
)

var specializationFactFieldPlans sync.Map // reflect.Type -> []int

// Scalar fields are copied by the enclosing struct assignment. Only fields
// that can contain a mutable object or an AST identity need a reflective walk.
func specializationFactFields(typ reflect.Type) []int {
	if cached, ok := specializationFactFieldPlans.Load(typ); ok {
		return cached.([]int)
	}
	var fields []int
	for i := 0; i < typ.NumField(); i++ {
		if specializationFactNeedsWalk(typ.Field(i).Type) {
			fields = append(fields, i)
		}
	}
	plan, _ := specializationFactFieldPlans.LoadOrStore(typ, fields)
	return plan.([]int)
}

func specializationFactNeedsWalk(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Slice, reflect.Map:
		return true
	case reflect.Array:
		return specializationFactNeedsWalk(typ.Elem())
	case reflect.Struct:
		return typ.Implements(reflect.TypeFor[ast.Node]()) || len(specializationFactFields(typ)) != 0
	default:
		return typ.Implements(reflect.TypeFor[ast.Node]())
	}
}

func specializationCanonicalLeaf(v reflect.Value) bool {
	if !v.IsValid() || !v.CanInterface() {
		return false
	}
	if _, ok := v.Interface().(Type); ok {
		return true
	}
	switch v.Interface().(type) {
	case *Symbol, *Scope:
		return true
	}
	return false
}

func (c *specializationContext) prepareFact(v reflect.Value, seen map[specializationPointer]bool) {
	if !v.IsValid() || specializationCanonicalLeaf(v) {
		return
	}
	if node, ok := specializationASTNode(v); ok {
		if _, isDecl := node.(ast.Decl); isDecl && !c.syntax.originals[node] {
			return
		}
		c.syntax.prepare(v)
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			c.prepareFact(v.Elem(), seen)
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if seen[key] {
			return
		}
		seen[key] = true
		c.prepareFact(settable(v.Elem()), seen)
	case reflect.Struct:
		fields := specializationFactFields(v.Type())
		if len(fields) == 0 {
			return
		}
		if !v.CanAddr() {
			addressable := reflect.New(v.Type()).Elem()
			addressable.Set(v)
			v = addressable
		}
		for _, i := range fields {
			c.prepareFact(settable(v.Field(i)), seen)
		}
	case reflect.Slice, reflect.Array:
		if specializationFactNeedsWalk(v.Type().Elem()) {
			for i := 0; i < v.Len(); i++ {
				c.prepareFact(v.Index(i), seen)
			}
		}
	case reflect.Map:
		if !v.IsNil() && (specializationFactNeedsWalk(v.Type().Key()) || specializationFactNeedsWalk(v.Type().Elem())) {
			key := specializationPointer{v.Type(), v.Pointer()}
			if seen[key] {
				return
			}
			seen[key] = true
			it := v.MapRange()
			for it.Next() {
				c.prepareFact(it.Key(), seen)
				c.prepareFact(it.Value(), seen)
			}
		}
	}
}

func (c *specializationContext) copyFact(v reflect.Value, memo map[specializationPointer]reflect.Value) reflect.Value {
	if !v.IsValid() || specializationCanonicalLeaf(v) {
		return v
	}
	if node, ok := specializationASTNode(v); ok {
		if private := c.syntax.nodes[node]; private != nil {
			return reflect.ValueOf(private)
		}
		if _, isDecl := node.(ast.Decl); isDecl {
			return v
		}
		return c.syntax.copy(v)
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(c.copyFact(v.Elem(), memo))
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if old, ok := memo[key]; ok {
			return old
		}
		out := reflect.New(v.Type().Elem())
		memo[key] = out
		out.Elem().Set(c.copyFact(settable(v.Elem()), memo))
		return out
	case reflect.Struct:
		fields := specializationFactFields(v.Type())
		if len(fields) == 0 {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for _, i := range fields {
			field := settable(out.Field(i))
			field.Set(c.copyFact(field, memo))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		if !specializationFactNeedsWalk(v.Type().Elem()) {
			reflect.Copy(out, v)
			return out
		}
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.copyFact(v.Index(i), memo))
		}
		return out
	case reflect.Array:
		if !specializationFactNeedsWalk(v.Type().Elem()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.copyFact(v.Index(i), memo))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		key := specializationPointer{v.Type(), v.Pointer()}
		if old, ok := memo[key]; ok {
			return old
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		memo[key] = out
		it := v.MapRange()
		for it.Next() {
			out.SetMapIndex(c.copyFact(it.Key(), memo), c.copyFact(it.Value(), memo))
		}
		return out
	}
	return v
}

func specializationASTNode(v reflect.Value) (ast.Node, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return nil, false
	}
	n, ok := v.Interface().(ast.Node)
	return n, ok
}
