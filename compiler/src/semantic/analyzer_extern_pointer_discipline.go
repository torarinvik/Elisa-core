package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// Extern POINTER discipline (docs/127 §3.7, D1 and D12), enforced only under -strict-externs.
//
// D1: an extern parameter or return typed `void&` / `void&?` (any mutability) is an UNTYPED
//     pointer: any handle passes for any other, and no contract can name what it points at. The
//     extern must instead use an opaque handle (`extern Name`) or be `@trusted("reason")`.
//
// D12: an extern that carries a contract must name EVERY pointer parameter in it; a pointer the
//     contract never mentions has nothing documented about the memory it hands over. Presence is
//     not coverage: `extern memcpy(dst: mutable u8&, src: u8&, n: usize) requires n > 0` must not
//     pass the gate, and it is reported once per uncovered parameter, at the parameter. A pointer parameter is any ref, optional ref, or `cstr`; opaque handles and bounded
//     views are typed on their own and do not count as uncovered.
func (a *Analyzer) checkExternPointerDiscipline(fn *ast.ExternFuncDecl, fnType *FuncType) {
	if a == nil || fn == nil || fnType == nil || !a.requireExternContracts || externHasTrustedAnnotation(fn) {
		return
	}
	for i, param := range fn.Params {
		if i >= len(fnType.Params) {
			break
		}
		if isUntypedPointerType(fnType.Params[i]) {
			a.errorf(param.Position, "extern function %q parameter %q is an untyped pointer (%s); declare an opaque handle with `extern Name` and use it instead of void, or mark the extern @trusted(\"reason\")", fn.Name, param.Name, fnType.Params[i])
		}
	}
	// D5: a reference to a SCALAR element (`u8&`, `mutable f32&`) beside an integer parameter is
	// the classic (pointer, length) pair with the two kept strangers: the type bounds nothing.
	// A reference to a struct is one object and a lone scalar reference is an out-parameter, so
	// neither fires. `@bounds` removes the length and turns the pointer into a view, which is
	// the fix the message names.
	if fnHasUnboundIntegerParam(fnType) {
		for i, param := range fn.Params {
			if i >= len(fnType.Params) {
				break
			}
			if elem, mutable, ok := unboundedScalarRefParam(fnType.Params[i]); ok {
				viewSpelling := "view[" + elem + "]"
				if mutable {
					viewSpelling = "mutable " + viewSpelling
				}
				a.errorf(param.Position, "extern function %q parameter %q is a pointer with no bounds; declare it as %s, bind a length with @bounds(%s, <length>), or mark the extern @trusted(\"reason\")", fn.Name, param.Name, viewSpelling, param.Name)
			}
		}
	}
	if fnType.Return != nil && isUntypedPointerType(fnType.Return) {
		a.errorf(fn.Pos(), "extern function %q returns an untyped pointer (%s); declare an opaque handle with `extern Name` and return it instead of void, or mark the extern @trusted(\"reason\")", fn.Name, fnType.Return)
	}
	hasContract := len(fn.Requires) > 0 || len(fn.EnsureValues) > 0 || len(fn.Ensures) > 0 || len(fn.Uses) > 0
	if !hasContract {
		return
	}
	mentioned := map[string]bool{}
	scanValueUsedIdents(reflect.ValueOf(fn.Requires), mentioned)
	scanValueUsedIdents(reflect.ValueOf(fn.EnsureValues), mentioned)
	scanValueUsedIdents(reflect.ValueOf(fn.Uses), mentioned)
	for _, clause := range fn.Ensures {
		if clause.Target.Root != "" {
			mentioned[clause.Target.Root] = true
		}
	}
	for i, param := range fn.Params {
		if i < len(fnType.Params) && isExternPointerParamType(fnType.Params[i]) && !mentioned[param.Name] {
			a.errorf(param.Position, "extern function %q pointer parameter %q is not covered by its contract; under -strict-externs every pointer parameter must be named by a `requires`/`ensure` clause, be an opaque handle, or the extern must be @trusted(\"reason\")", fn.Name, param.Name)
		}
	}
}

// isUntypedPointerType reports a `void&`, `mutable void&`, or their optional forms.
func isUntypedPointerType(t Type) bool {
	if opt, ok := t.(*OptionalType); ok {
		t = opt.Value
	}
	ref, ok := t.(*RefType)
	return ok && isVoidType(ref.Elem)
}

// isExternPointerParamType reports a parameter that hands native code an address whose extent
// the type does not bound: any ref, any optional ref, or an unbounded `cstr`. Opaque handles are
// typed on their own; views carry their length.
func isExternPointerParamType(t Type) bool {
	if opt, ok := t.(*OptionalType); ok {
		t = opt.Value
	}
	switch typ := t.(type) {
	case *RefType:
		_, opaque := typ.Elem.(*OpaqueType)
		return !opaque
	case *CStrType:
		// `cstr`: an unbounded NUL-terminated pointer.
		return true
	}
	return false
}


// unboundedScalarRefParam reports a `T&` / `mutable T&` (optionally nullable) whose referent is a
// scalar builtin: the shape C uses for a buffer whose length travels separately.
func unboundedScalarRefParam(t semantic_Type) (string, bool, bool) {
	if opt, ok := t.(*OptionalType); ok {
		t = opt.Value
	}
	ref, ok := t.(*RefType)
	if !ok || ref == nil {
		return "", false, false
	}
	elem, ok := ref.Elem.(*BuiltinType)
	if !ok || elem == nil || !isScalarBuiltinName(elem.Name) {
		return "", false, false
	}
	return elem.Name, ref.Mutable, true
}

type semantic_Type = Type

func isScalarBuiltinName(name string) bool {
	switch name {
	case "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "f32", "f64", "int", "uint", "usize", "isize", "uintptr", "bool", "char":
		return true
	}
	return false
}

func isIntegerBuiltinName(name string) bool {
	switch name {
	case "i8", "i16", "i32", "i64", "u8", "u16", "u32", "u64", "int", "uint", "usize", "isize", "uintptr":
		return true
	}
	return false
}

// fnHasUnboundIntegerParam reports an integer-typed explicit parameter (one `@bounds` did not
// already consume; consumed lengths are gone from Params).
func fnHasUnboundIntegerParam(fn *FuncType) bool {
	for _, p := range fn.Params {
		switch t := p.(type) {
		case *BuiltinType:
			if isIntegerBuiltinName(t.Name) {
				return true
			}
		case *BitIntType:
			return true
		}
	}
	return false
}
