package semantic

import (
	"fmt"
	"reflect"

	"elisacore/src/ast"
	"elisacore/src/lexer"
)

// A plain `assert COND` is debug-gated: codegen erases it above -O0 (backend emitStmt), exactly
// like `requires`/`ensure` contracts (docs/78 erasure tiers, docs/99). A condition with an
// observable side effect would therefore change program behaviour between debug and release —
// `assert decode(&input, &out)` silently skipped the decode at -O3. Reject such conditions the
// same way contract clauses are rejected ("ensure clause must be pure"): any call that writes
// through a `mutable` reference parameter, and any effect that is not benign under erasure
// (assertBenignEffect: aborts, temporary allocation, global reads, Unsafe grants).
// Tier: WARNING (not an error, not even under the strict defaults). The tree relies on the idiom
// today — std smokes (`assert s.remove(3)`, `assert d.get(k) != null` with a `mutable` receiver),
// deque_ports/allocator_ports, and runtime tests whose whole body is `assert check() == 0` — and
// making it an error is a language-policy call for the owner. The warning names the release hazard.
func (a *Analyzer) checkAssertConditionPurity(pos lexer.Pos, cond ast.Expr, introduced []ast.PermissionRef) {
	for _, ref := range introduced {
		if assertBenignEffect(ref) {
			continue
		}
		a.warnf(pos, "assert condition must be pure but uses the `%s` effect; asserts are erased in optimized builds, so the effect would only happen in debug builds — bind the result first (`ok: bool = f(...)`, then `assert ok`)", lawEffectName(ref))
		return
	}
	if name, ok := a.assertCondMutatingCall(cond); ok {
		a.warnf(pos, "%s", fmt.Sprintf("assert condition must be pure but calls %q, which writes through a `mutable` reference parameter; asserts are erased in optimized builds, so the call would only run in debug builds — bind the result first (`ok: bool = %s(...)`, then `assert ok`)", name, name))
	}
}

// assertBenignEffect: effects whose erasure is unobservable — aborting (a failing assert aborts
// anyway), temporary allocation, reads of globals, and Unsafe.* capability grants (permissions, not
// effects; the check that matters for them is the mutable-ref walk).
func assertBenignEffect(ref ast.PermissionRef) bool {
	switch ref.Name {
	case "Abort", "Unsafe":
		return true
	case "Memory":
		return ref.Member == "Allocate"
	case "Global":
		return ref.Member == "Read"
	}
	return false
}

// assertCondMutatingCall finds a call in expr whose callee takes a `mutable T&` parameter.
func (a *Analyzer) assertCondMutatingCall(expr ast.Expr) (string, bool) {
	name, found := "", false
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		if found || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || !v.CanInterface() {
				return
			}
			if call, ok := v.Interface().(*ast.CallExpr); ok && call != nil {
				if ft, ok := a.exprTypes[call.Func].(*FuncType); ok && ft != nil {
					for _, p := range ft.Params {
						if ref, ok := p.(*RefType); ok && ref.Mutable {
							name, found = callIdentName(call), true
							if name == "" {
								name = ft.Name
							}
							return
						}
					}
				}
			}
			walk(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(reflect.ValueOf(expr))
	return name, found
}
