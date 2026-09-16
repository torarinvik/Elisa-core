package semantic

import (
	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// docs/127 D9 — an `extern enum` mirrors a C enum, whose value set is open at the ABI.
//
// Elisa checks `match` exhaustively over an enum's members. That proof is sound for a value
// the language constructed and unsound for one reinterpreted from a foreign integer: the arms
// cover every member, the optimizer is entitled to assume the value is one of them, and an
// unnamed value then walks off the end of the switch. So the reinterpretation is the thing to
// forbid — `Name.from_c(raw)` validates instead, and returns the failure as an error union
// the caller cannot drop on the floor.

// IsForeign reports an `extern enum` whose members mirror a C header.
func (t *ConstEnumType) IsForeign() bool { return t != nil && t.Decl != nil && t.Decl.Foreign }

// IsOpenForeign reports an `@open extern enum`: the C API may name values this list does not,
// so there is nothing to validate and `match` carries the obligation instead (a default arm).
func (t *ConstEnumType) IsOpenForeign() bool { return t.IsForeign() && t.Decl.Open }

// inForeignEnumValidator reports whether analysis is currently inside the synthesized
// validator for enumName, the one place its raw reinterpretation is legitimate. Mirrors
// isSelfCastHook: without the exemption the validator's own body would be its first victim.
func (a *Analyzer) inForeignEnumValidator(enumName string) bool {
	if a == nil || a.currentFuncDecl == nil || enumName == "" {
		return false
	}
	return a.currentFuncDecl.Name == ast.ForeignEnumValidatorName(unqualifiedName(enumName))
}

// rejectForeignEnumReinterpret reports D9 for a raw integer reinterpreted as a closed foreign
// enum. Reports and returns true when the conversion is the unchecked one; false otherwise,
// leaving the ordinary cast rules in charge.
func (a *Analyzer) rejectForeignEnumReinterpret(pos lexer.Pos, src, dst Type) bool {
	if a == nil || IsInvalidType(src) || IsInvalidType(dst) {
		return false
	}
	target, ok := dst.(*ConstEnumType)
	if !ok || !target.IsForeign() || target.IsOpenForeign() {
		return false
	}
	// Enum-to-itself and member construction are not reinterpretations of foreign data.
	if !IsNumericType(src) || SameType(src, dst) {
		return false
	}
	if a.inForeignEnumValidator(target.Name) {
		return false
	}
	short := unqualifiedName(target.Name)
	a.errorf(pos, "cannot reinterpret a C value as extern enum %q: it is not known to be one of its members; construct it through %s.from_c(...) so out-of-range values are rejected", short, short)
	return true
}

// rewriteForeignEnumValidatorCallee turns the surface spelling `Name.from_c(raw)` into a call
// of the synthesized validator. Elisa has no static methods, so `Name.from_c` would otherwise
// resolve as const-enum member access and fail; this is the one name that resolves that way.
// Idempotent: the rewritten callee is an Ident and no longer matches.
func (a *Analyzer) rewriteForeignEnumValidatorCallee(expr *ast.CallExpr) {
	if a == nil || expr == nil {
		return
	}
	fieldExpr, ok := expr.Func.(*ast.FieldExpr)
	if !ok || fieldExpr == nil || fieldExpr.Field != "from_c" {
		return
	}
	typeName, ok := qualifiedTypePathFromExpr(fieldExpr.Object)
	if !ok || typeName == "" {
		return
	}
	resolved, _, ok := a.lookupVisibleType(typeName)
	if !ok {
		if resolved, ok = a.namedTypes[typeName]; !ok {
			return
		}
	}
	constEnumType, ok := resolved.(*ConstEnumType)
	if !ok || !constEnumType.IsForeign() {
		return
	}
	if constEnumType.IsOpenForeign() {
		a.errorf(expr.Pos(), "extern enum %q is @open: every integer of its storage type is a valid value, so there is nothing for from_c to reject; convert with %s(raw) and give `match` a default arm", unqualifiedName(constEnumType.Name), unqualifiedName(constEnumType.Name))
		// Recover as the plain conversion the message recommends, so this reports once.
		expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: typeName}
		return
	}
	name := ast.ForeignEnumValidatorName(unqualifiedName(constEnumType.Name))
	expr.Func = &ast.Ident{Position: fieldExpr.Position, Name: name}
}

func unqualifiedName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}
