package semantic

import "elisacore/src/ast"

// checkLawContract enforces what makes a `law` a sound predicate rather than an arbitrary
// function (docs/85 §2, §9.5): it must return `bool`, take at least one value parameter (its
// subject), and be PURE — no effects. Purity is checked via the function's inferred effect set
// (the same set `@hot` is judged against), so a law cannot observe time/IO/randomness or mutate;
// that is what lets the compiler treat the predicate as a reorderable, cacheable fact and lets
// `is` apply it freely in type, flow, and contract position. Totality is covered by the existing
// progress/recursion checks that run over every function body.
func (a *Analyzer) checkLawContract(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || !fn.IsLaw {
		return
	}
	if !IsBoolType(fnType.Return) {
		a.errorf(fn.Pos(), "law %q must be a predicate returning bool, got %s", fn.Name, typeString(fnType.Return))
	}
	if len(fnType.Params) == 0 {
		a.errorf(fn.Pos(), "law %q needs a subject: give it at least one value parameter (conventionally `self`)", fn.Name)
	}
	// Purity: a law's inferred effect set must be empty. PermissionRefs carries the transitive
	// effects (the same set the @hot contract is judged against); any entry means the predicate
	// is impure and cannot be a sound, freely-applicable fact.
	for _, ref := range fnType.PermissionRefs {
		a.errorf(fn.Pos(), "law %q must be pure but uses the `%s` effect; a predicate may not perform effects (no IO, allocation, mutation, time, or randomness)", fn.Name, lawEffectName(ref))
		break
	}
}

// lawEffectName renders an effect reference for the law-purity diagnostic.
func lawEffectName(ref ast.PermissionRef) string {
	if ref.Member != "" {
		return ref.Name + "." + ref.Member
	}
	return ref.Name
}

// typeString renders a type for diagnostics, tolerating nil.
func typeString(t Type) string {
	if t == nil {
		return "void"
	}
	return t.String()
}
