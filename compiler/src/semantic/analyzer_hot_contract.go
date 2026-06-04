package semantic

import "elisacore/src/ast"

// The @hot fast contract (docs/70). @hot already places a function as hot for codegen; on
// top of that it is a *contract*: the hot path must do no allocation and no raw-pointer
// chasing / indirect dispatch. Friction lands on the slow pattern, never the fast one —
// fast-unsafe ops that REMOVE overhead (Unsafe.UncheckedIndex, AssumeProgress) and cold
// branches (Abort.Panic) stay allowed, so a tight numeric/handle loop needs no ceremony.
//
// Because effect inference is transitive (a caller's effects include its callees'), a @hot
// function that calls anything which allocates or chases raw pointers is rejected too — the
// guarantee holds through call boundaries.

// hotContractBannedEffect reports whether an effect is forbidden inside a @hot function,
// with a short human reason for the diagnostic.
func hotContractBannedEffect(ref ast.PermissionRef) (string, bool) {
	switch ref.Name {
	case "Memory":
		switch ref.Member {
		case "Allocate":
			return "allocates (region growth / realloc) on the hot path", true
		case "Release":
			return "frees memory on the hot path", true
		}
	case "Unsafe":
		switch ref.Member {
		case "PointerCast", "PointerArithmetic", "GuestHostPointerCast", "Alias",
			"BufferReinterpret", "StaleRef", "RawExtern", "MutableGlobal", "Leak",
			"IndirectCall", "SegmentMutation", "GuestSegmentInstall", "ThreadShare":
			return "chases raw pointers or dispatches indirectly", true
		}
	}
	return "", false
}

// checkHotContract enforces the @hot fast contract: a hot function may use no allocation and
// no raw-pointer / indirect-dispatch effect (transitively). This makes the hot path
// allocation-free and pointer-chase-free by construction.
func (a *Analyzer) checkHotContract(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || !funcHasAnnotation(fn, "hot") {
		return
	}
	seen := map[string]bool{}
	for _, ref := range fnType.PermissionRefs {
		reason, banned := hotContractBannedEffect(ref)
		if !banned {
			continue
		}
		key := ref.Name + "." + ref.Member
		if seen[key] {
			continue
		}
		seen[key] = true
		a.errorf(fn.Pos(), "@hot function %q %s (via the `%s` effect); the hot path must stay allocation-free and pointer-chase-free. Preallocate outside the hot region (reserve), use a Store/handle instead of raw pointers, or remove @hot", fn.Name, reason, key)
	}
}
