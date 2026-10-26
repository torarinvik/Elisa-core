package semantic

import (
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

const (
	proofBindingVisitNode uint8 = iota
	proofBindingVisitStringField
	proofBindingVisitString
	proofBindingVisitPositionFile
)

func proofBindingNextOwner(value any, owner ast.Decl) ast.Decl {
	if decl, ok := value.(ast.Decl); ok {
		return decl
	}
	return owner
}

// proofBindingWalkReflect preserves the original proof-binding walk for
// unsupported or inaccessible types. In particular, maps and arrays remain
// leaves, slices are traversed, and only actual pointer edges enter seen.
func proofBindingWalkReflect(v reflect.Value, visit func(any, reflect.Type, ast.Decl, uint8, string, string, bool), seen map[uintptr]bool, owner ast.Decl) {
	if !v.IsValid() {
		return
	}
	if proofBindingPositionSchemaValid && v.Type() == reflect.TypeFor[lexer.Pos]() {
		visit(nil, v.Type(), owner, proofBindingVisitPositionFile, "File", v.Field(0).String(), false)
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			proofBindingWalkReflect(v.Elem(), visit, seen, owner)
		}
	case reflect.Pointer:
		if v.IsNil() || seen[v.Pointer()] {
			return
		}
		seen[v.Pointer()] = true
		if v.CanInterface() {
			value := v.Interface()
			owner = proofBindingNextOwner(value, owner)
			switch value.(type) {
			case ast.Decl, *ast.ExprBlock, *ast.RegionStmt, *ast.VarDeclStmt:
				visit(value, v.Type(), owner, proofBindingVisitNode, "", "", true)
			}
		}
		proofBindingWalkReflect(v.Elem(), visit, seen, owner)
	case reflect.Struct:
		var parent any
		addressable := v.CanAddr() && v.Addr().CanInterface()
		if addressable {
			parent = v.Addr().Interface()
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			if field.Kind() == reflect.String {
				visit(parent, v.Type(), owner, proofBindingVisitStringField, v.Type().Field(i).Name, field.String(), addressable)
				continue
			}
			proofBindingWalkReflect(field, visit, seen, owner)
		}
	case reflect.String:
		visit(nil, nil, owner, proofBindingVisitString, "", v.String(), false)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := 0; i < v.Len(); i++ {
			proofBindingWalkReflect(v.Index(i), visit, seen, owner)
		}
	}
}
