package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// bodyAllocatesInferredContainers reports whether the statements contain a region-less
// container literal or comprehension outside any explicit region/in-store scope — the
// allocations a synthesized ambient region would capture.
func bodyAllocatesInferredContainers(stmts []ast.Stmt) bool {
	found := false
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			switch n := v.Interface().(type) {
			case *ast.RegionStmt:
				if len(n.Body) != 0 {
					return
				}
			case *ast.InStoreStmt:
				return
			case *ast.LambdaExpr:
				return
			case *ast.ListLitExpr:
				if n.Owner == nil {
					found = true
					return
				}
			case *ast.ListComprehensionExpr:
				if n.Owner == nil {
					found = true
					return
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(stmts))
	return found
}

// headerOnlyDarrayElemName reports a type expression spelled `darray[E]` where E is `sview` or
// a non-byte scalar, returning E's name. Such a container's storage holds no bytes a view could
// point into: what a callee can hand back from it is a copy of an element, never its storage.
func headerOnlyDarrayElemName(t ast.TypeExpr) (string, bool) {
	bt, ok := t.(*ast.BuiltinTypeExpr)
	if !ok || bt == nil || bt.Name != "darray" || len(bt.TypeArgs) != 1 || bt.Region != "" {
		return "", false
	}
	name := ""
	switch elem := bt.TypeArgs[0].(type) {
	case *ast.NamedType:
		if elem == nil || elem.Region != "" {
			return "", false
		}
		name = elem.Name
	case *ast.BuiltinTypeExpr:
		if elem == nil || len(elem.TypeArgs) != 0 || elem.Region != "" {
			return "", false
		}
		name = elem.Name
	default:
		return "", false
	}
	switch name {
	case "sview", "bool", "u16", "u32", "u64", "usize", "i16", "i32", "i64", "isize", "f32", "f64", "char":
		return name, true
	}
	return "", false
}

// typeMayReachDarrayStorage reports whether a value of type t could hold (or point at) the
// storage of a `darray[elem]`: such a header anywhere inside it, any reference or generic view
// (which may point at the container or its element slots), or anything opaque. A PACKED enum
// value is a handle into its store: its payload rows live in that store, whose own region every
// store into it is checked against, so the handle itself reaches no caller container.
func typeMayReachDarrayStorage(t Type, elem string) bool {
	return typeMayReachDarrayStorageRec(t, elem, map[Type]bool{})
}

func typeMayReachDarrayStorageRec(t Type, elem string, seen map[Type]bool) bool {
	if t == nil {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true
	switch tt := t.(type) {
	case *SViewType, *CStrType, *BitIntType, *ConstEnumType, *BitGroupType, *IDType, *NullType, *NeverType:
		return false
	case *BuiltinType:
		switch tt.Name {
		case "u8", "u16", "u32", "u64", "usize", "i8", "i16", "i32", "i64", "isize", "f32", "f64", "bool", "char", "void":
			return false
		}
		return true
	case *ArrayType:
		return typeMayReachDarrayStorageRec(tt.Elem, elem, seen)
	case *OptionalType:
		return typeMayReachDarrayStorageRec(tt.Value, elem, seen)
	case *ErrorUnionType:
		return typeMayReachDarrayStorageRec(tt.Value, elem, seen)
	case *AggregateStateType:
		return typeMayReachDarrayStorageRec(tt.Base, elem, seen)
	case *TupleType:
		for _, field := range tt.Fields {
			if typeMayReachDarrayStorageRec(field.Type, elem, seen) {
				return true
			}
		}
		return false
	case *StructType:
		if len(tt.TypeParams) > 0 {
			return true
		}
		for _, field := range tt.Fields {
			if typeMayReachDarrayStorageRec(field.Type, elem, seen) {
				return true
			}
		}
		return false
	case *EnumType:
		if tt.Packed {
			return false
		}
		for _, field := range tt.Common {
			if typeMayReachDarrayStorageRec(field.Type, elem, seen) {
				return true
			}
		}
		for _, variant := range tt.Variants {
			for _, payload := range variant.Payload {
				if typeMayReachDarrayStorageRec(payload, elem, seen) {
					return true
				}
			}
		}
		return false
	case *DArrayType:
		if tt.Elem == nil {
			return true
		}
		// A nested header with the same element type can directly retain the source
		// darray's storage even when that element type is a user-defined aggregate.
		if tt.Elem.String() == elem {
			return true
		}
		switch e := StripAggregateStateType(tt.Elem).(type) {
		case *SViewType:
			if elem == "sview" {
				return true
			}
		case *BuiltinType:
			if e.Name == elem {
				return true
			}
		}
		return typeMayReachDarrayStorageRec(tt.Elem, elem, seen)
	case *DictType:
		return typeMayReachDarrayStorageRec(tt.Key, elem, seen) || typeMayReachDarrayStorageRec(tt.Value, elem, seen)
	case *SetType:
		return typeMayReachDarrayStorageRec(tt.Elem, elem, seen)
	}
	return true
}
