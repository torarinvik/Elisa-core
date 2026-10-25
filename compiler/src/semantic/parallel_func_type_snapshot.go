package semantic

import (
	"elisacore/src/ast"
	"reflect"
)

// Snapshot mutable function metadata without cloning nominal declarations or
// syntax. Unlike deepCloneValue (worker map COW), publication must detach slice
// backing too. Structural type wrappers are copied so nested function signatures
// cannot expose an owner's mutable summaries through Params or Return.
func snapshotFuncTypeMetadata(value reflect.Value) reflect.Value {
	return snapshotFuncTypeMetadataRoot(value, nil, nil)
}

// Seed the canonical destination before walking metadata. A recursive function
// signature must point back to its new root rather than a second cloned root.
func snapshotFuncTypeForDestination(source, destination *FuncType) reflect.Value {
	return snapshotFuncTypeMetadataRoot(reflect.ValueOf(source).Elem(), source, destination)
}

func snapshotFuncTypeMetadataRoot(value reflect.Value, source, destination *FuncType, syntaxCopy ...func(reflect.Value) reflect.Value) reflect.Value {
	type pointerKey struct {
		typ reflect.Type
		ptr uintptr
	}
	seen := map[pointerKey]reflect.Value{}
	if source != nil && destination != nil {
		root := reflect.ValueOf(source)
		seen[pointerKey{root.Type(), root.Pointer()}] = reflect.ValueOf(destination)
	}
	var copyValue func(reflect.Value) reflect.Value
	copyValue = func(v reflect.Value) reflect.Value {
		if len(syntaxCopy) != 0 && ((v.Kind() == reflect.Pointer && v.Type().Elem().PkgPath() == astGraphPkgPath) || (v.Kind() == reflect.Struct && v.Type().PkgPath() == astGraphPkgPath)) {
			// Factory/declaration references denote canonical symbols. Expressions
			// and structural syntax in defaults/contracts need private ownership.
			if v.CanInterface() {
				if _, declaration := v.Interface().(ast.Decl); declaration {
					return v
				}
			}
			return syntaxCopy[0](v)
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return v
			}
			copyPointer := false
			if typ, ok := v.Interface().(Type); ok {
				switch typ.(type) {
				case *StructType, *EnumType, *ConstEnumType, *OpaqueType, *BitGroupType, *BuiltinType, *BitIntType, *TypeParamType, *ErrorSetType:
					return v // canonical nominal/leaf type identity (BitGroup equality is pointer identity)
				default:
					copyPointer = true
				}
			} else if v.Type() == reflect.TypeOf((*SpecSignature)(nil)) {
				copyPointer = true
			}
			if !copyPointer {
				return v
			} // ASTs and other declaration-owned roots
			key := pointerKey{v.Type(), v.Pointer()}
			if prior, ok := seen[key]; ok {
				return prior
			}
			out := reflect.New(v.Type().Elem())
			seen[key] = out
			out.Elem().Set(copyValue(v.Elem()))
			return out
		case reflect.Interface:
			if v.IsNil() {
				return v
			}
			out := reflect.New(v.Type()).Elem()
			out.Set(copyValue(v.Elem()))
			return out
		case reflect.Struct:
			out := reflect.New(v.Type()).Elem()
			out.Set(v)
			for i := 0; i < out.NumField(); i++ {
				field := settable(out.Field(i))
				field.Set(copyValue(field))
			}
			return out
		case reflect.Slice:
			if v.IsNil() {
				return v
			}
			out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := 0; i < v.Len(); i++ {
				out.Index(i).Set(copyValue(v.Index(i)))
			}
			return out
		case reflect.Array:
			out := reflect.New(v.Type()).Elem()
			for i := 0; i < v.Len(); i++ {
				out.Index(i).Set(copyValue(v.Index(i)))
			}
			return out
		case reflect.Map:
			if v.IsNil() {
				return v
			}
			out := reflect.MakeMapWithSize(v.Type(), v.Len())
			iter := v.MapRange()
			for iter.Next() {
				out.SetMapIndex(iter.Key(), copyValue(iter.Value()))
			}
			return out
		default:
			return v
		}
	}
	return copyValue(value)
}
