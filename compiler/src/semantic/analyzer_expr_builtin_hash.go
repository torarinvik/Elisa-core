package semantic

import "elisacore/src/ast"

// analyzeHashBuiltinCall type-checks the compiler-emitted `ctx_hash_value(key)` builtin used by
// the runtime-backed hash dict. It returns a u64 hash of a hashable key. Like `==`, it is valid
// over a generic key parameter (the concrete type is known after monomorphization, where the
// backend picks content-hash for cstr vs value-hash for scalars); the accepted key types mirror
// dictRuntimeBackedKeyType. The name is `ctx_*`-namespaced so it never shadows a user `hash`.
func (a *Analyzer) analyzeHashBuiltinCall(expr *ast.CallExpr) (Type, bool) {
	if expr == nil || callIdentName(expr) != "ctx_hash_value" {
		return nil, false
	}
	u64Type := a.namedTypes["u64"]
	if len(expr.Args) != 1 {
		for _, arg := range expr.Args {
			a.analyzeExpr(arg)
		}
		a.errorf(expr.Pos(), "ctx_hash_value expects 1 argument, got %d", len(expr.Args))
		return u64Type, true
	}
	argType := a.analyzeExpr(expr.Args[0])
	if !IsInvalidType(argType) && !dictRuntimeBackedKeyType(argType) {
		a.errorf(expr.Args[0].Pos(), "ctx_hash_value requires a hashable key (cstr, an integer type, bool, or a const enum), got %s", diagnosticTypeString(argType))
	}
	a.recordBuiltinHelperFuncType(expr, "ctx_hash_value", u64Type)
	return u64Type, true
}
