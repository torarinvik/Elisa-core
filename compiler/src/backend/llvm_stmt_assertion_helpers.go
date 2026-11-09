//go:build cgo

package backend

import (
	"elisacore/src/ast"
	"elisacore/src/semantic"
	"reflect"
)

// backendAssertedCondition mirrors the analyzer's `assertedCondition`: a plain `assert(COND)` /
// `ASSERT(COND)` is a single-argument call to the reserved `assert` identifier, not a real function
// call. The analyzer already treats it as an assertion; the backend must agree so it lowers to a
// debug-gated check rather than trying (and failing) to resolve `assert` as a callee.
func backendAssertedCondition(result *semantic.Result, expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || (ident.Name != "assert" && ident.Name != "ASSERT") {
		return nil, false
	}
	if result == nil || !result.IsCompilerBuiltinHelperCall(call, ident.Name) {
		return nil, false
	}
	return call.Args[0], true
}

// paramNames lists the parameters that can own or reference caller-visible storage: the escape
// roots of adoptedEscapeNamesForBody.
func paramNames(params []ast.ParamDecl) []string {
	names := make([]string, 0, len(params))
	for _, param := range params {
		if named, ok := param.Type.(*ast.NamedType); ok && named != nil && adoptedEscapeScalarTypes[named.Name] {
			continue
		}
		names = append(names, param.Name)
	}
	return names
}

// exprNamesAnyOf reports whether expr mentions an identifier in names.
func exprNamesAnyOf(expr ast.Expr, names map[string]bool) bool {
	if expr == nil || len(names) == 0 {
		return false
	}
	found := false
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if ident, ok := v.Interface().(*ast.Ident); ok && ident != nil {
				found = names[ident.Name]
				return
			}
			walk(v.Elem())
		case reflect.Struct:
			for _, i := range astWalkFields(v.Type()) {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			if !astTypeMayHoldNodes(v.Type().Elem()) {
				break
			}
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(expr))
	return found
}

// adoptedEscapeReadOnlyMethods are builtin methods that never store an operand into another.
var adoptedEscapeReadOnlyMethods = map[string]bool{
	"count": true, "len": true, "is_empty": true, "contains": true, "get": true, "at": true,
	"first": true, "last": true, "starts_with": true, "ends_with": true, "index_of": true,
	"find": true, "has": true, "has_key": true, "contains_key": true, "eq": true, "equals": true,
	"compare": true, "cmp": true, "hash": true, "reserve": true, "clear": true, "pop": true,
}

// adoptedEscapeElementMethods are the read-only methods whose result is an element that shares
// the receiver's backing storage (`xs.pop()` of a darray of darrays): a returned result keeps the
// receiver in the caller arena.
var adoptedEscapeElementMethods = map[string]bool{
	"get": true, "at": true, "first": true, "last": true, "pop": true, "find": true,
}

// adoptedEscapeScalarTypes are parameter types that cannot own or reference storage, so a
// parameter of one is never an escape root.
var adoptedEscapeScalarTypes = map[string]bool{
	"i8": true, "i16": true, "i32": true, "i64": true, "u8": true, "u16": true, "u32": true,
	"u64": true, "usize": true, "isize": true, "f32": true, "f64": true, "bool": true, "char": true,
}
