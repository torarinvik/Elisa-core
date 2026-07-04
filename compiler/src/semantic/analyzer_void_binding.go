package semantic

import (
	"elisacore/src/lexer"
)

// docs/119 §1: `void` is a type, not a value. A binding can never hold a void —
// without these checks a `x = voidFn()` falls through to raw LLVM errors
// ("Cannot allocate unsized type") instead of a diagnostic. `void&` stays legal
// (opaque-handle builtins are typed through it), as does `_ = voidFn()` (a
// discard, not a binding).

// declaredTypeHasBareVoid reports whether a *declared* binding type is bare void
// or a container whose element/value type is bare void (`darray[void]`,
// `dict[K, void]`, `[N]void`, `set[void]`, a tuple slot). Refs to void are exempt.
func declaredTypeHasBareVoid(t Type) bool {
	switch typ := t.(type) {
	case *BuiltinType:
		return isVoidType(typ)
	case *ArrayType:
		return declaredTypeHasBareVoid(typ.Elem)
	case *DArrayType:
		return declaredTypeHasBareVoid(typ.Elem)
	case *SetType:
		return declaredTypeHasBareVoid(typ.Elem)
	case *DictType:
		return declaredTypeHasBareVoid(typ.Key) || declaredTypeHasBareVoid(typ.Value)
	case *TupleType:
		for _, f := range typ.Fields {
			if declaredTypeHasBareVoid(f.Type) {
				return true
			}
		}
	}
	return false
}

// checkVoidBinding rejects binding a void-typed value or declaring a binding
// with a void-bearing type. Returns true when it reported (the caller should
// then skip assignability noise on the same decl).
func (a *Analyzer) checkVoidBinding(pos lexer.Pos, name string, declType, valueType Type, hasValue bool) bool {
	if declType != nil && declaredTypeHasBareVoid(declType) {
		a.errorf(pos, "variable %q: void is not a bindable type", name)
		return true
	}
	if hasValue && name != "_" && valueType != nil && isVoidType(valueType) {
		a.errorf(pos, "cannot bind void expression to %q (the initializer produces no value)", name)
		return true
	}
	return false
}

// checkVoidTupleBinding applies the void-binding rule to one destructured
// tuple element.
func (a *Analyzer) checkVoidTupleBinding(pos lexer.Pos, name string, fieldType Type) {
	if name != "_" && fieldType != nil && isVoidType(fieldType) {
		a.errorf(pos, "cannot bind void expression to %q (tuple element produces no value)", name)
	}
}
