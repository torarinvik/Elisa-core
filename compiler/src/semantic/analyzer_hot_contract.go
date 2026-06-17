package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// The @hot fast contract (docs/70). @hot already places a function as hot for codegen; on
// top of that it is a *contract* on what the hot path may do.
//
// Default @hot is the strict fast-path spelling: it forbids raw-pointer chasing, indirect
// dispatch, and allocation/free. @hot(alloc) is the explicit opt-in for allocation-bearing
// hot code; it keeps hot codegen metadata but still bans raw-pointer/indirect-dispatch effects.
//
// Fast-unsafe ops that REMOVE overhead (Unsafe.UncheckedIndex, AssumeProgress) and cold
// branches (Abort.Panic) stay allowed in both. The check is transitive — a @hot function
// that calls anything which chases pointers or allocates is rejected too unless it opted into
// @hot(alloc) for allocation.

// hotContractBannedEffect reports the effects forbidden by EVERY @hot function: raw-pointer
// chasing and indirect dispatch.
func hotContractBannedEffect(ref ast.PermissionRef) (string, bool) {
	if ref.Name == "Unsafe" {
		switch ref.Member {
		case "PointerCast", "PointerArithmetic", "GuestHostPointerCast", "Alias",
			"BufferReinterpret", "StaleRef", "RawExtern", "MutableGlobal", "Leak",
			"IndirectCall", "SegmentMutation", "GuestSegmentInstall", "ThreadShare":
			return "chases raw pointers or dispatches indirectly", true
		}
	}
	return "", false
}

// hotAllocationBannedEffect reports allocation/free effects forbidden by default in @hot.
func hotAllocationBannedEffect(ref ast.PermissionRef) (string, bool) {
	if ref.Name == "Memory" && (ref.Member == "Allocate" || ref.Member == "Release") {
		return "allocates or frees on the hot path", true
	}
	return "", false
}

// hotAnnotationAllowsAllocation reports whether a function carries `@hot(alloc)`.
func hotAnnotationAllowsAllocation(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	for _, ann := range fn.Annotations {
		if ann.Name != "hot" {
			continue
		}
		for _, arg := range ann.Args {
			if strings.EqualFold(strings.TrimSpace(arg), "alloc") {
				return true
			}
		}
	}
	return false
}

// checkHotContract enforces the @hot contract against a function's finalized (transitive)
// permission set.
func (a *Analyzer) checkHotContract(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil || !funcHasAnnotation(fn, "hot") {
		return
	}
	a.collectHotDisjointKernelCandidate(fn, fnType)
	allowAllocation := hotAnnotationAllowsAllocation(fn)
	seen := map[string]bool{}
	for _, ref := range fnType.PermissionRefs {
		reason, banned := hotContractBannedEffect(ref)
		fix := "use a Store/handle instead of raw pointers, or drop @hot"
		if !banned && !allowAllocation {
			if r, ok := hotAllocationBannedEffect(ref); ok {
				reason, banned = r, true
				fix = "preallocate outside the hot region (reserve), or use @hot(alloc) if allocation is intentionally part of this hot function"
			}
		}
		if !banned {
			continue
		}
		key := ref.Name + "." + ref.Member
		if seen[key] {
			continue
		}
		seen[key] = true
		label := "@hot"
		if allowAllocation {
			label = "@hot(alloc)"
		}
		a.errorf(fn.Pos(), "%s function %q %s (via the `%s` effect); %s", label, fn.Name, reason, key, fix)
	}
}
