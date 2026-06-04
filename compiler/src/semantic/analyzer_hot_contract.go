package semantic

import (
	"strings"

	"elisacore/src/ast"
)

// The @hot fast contract (docs/70). @hot already places a function as hot for codegen; on
// top of that it is a *contract* on what the hot path may do.
//
// Default @hot forbids the genuinely slow things — raw-pointer chasing and indirect
// dispatch (cache-hostile, unpredictable). It ALLOWS region allocation, which is the cheap
// frictionless default (region alloc carries no effect ceremony — see isAmbientPermission).
//
// @hot(noalloc) is the strict variant: it additionally forbids region allocation/growth, for
// a truly zero-allocation kernel (you preallocate with `reserve` and the loop only computes).
//
// Fast-unsafe ops that REMOVE overhead (Unsafe.UncheckedIndex, AssumeProgress) and cold
// branches (Abort.Panic) stay allowed in both. The check is transitive — a @hot function
// that calls anything which chases pointers (or, under noalloc, allocates) is rejected too.

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

// hotNoAllocBannedEffect reports the additional effect forbidden only by @hot(noalloc):
// allocation/free on the hot path.
func hotNoAllocBannedEffect(ref ast.PermissionRef) (string, bool) {
	if ref.Name == "Memory" && (ref.Member == "Allocate" || ref.Member == "Release") {
		return "allocates or frees on the hot path", true
	}
	return "", false
}

// hotAnnotationRequestsNoAlloc reports whether a function carries `@hot(noalloc)`.
func hotAnnotationRequestsNoAlloc(fn *ast.FuncDecl) bool {
	if fn == nil {
		return false
	}
	for _, ann := range fn.Annotations {
		if ann.Name != "hot" {
			continue
		}
		for _, arg := range ann.Args {
			if strings.EqualFold(strings.TrimSpace(arg), "noalloc") {
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
	noalloc := hotAnnotationRequestsNoAlloc(fn)
	seen := map[string]bool{}
	for _, ref := range fnType.PermissionRefs {
		reason, banned := hotContractBannedEffect(ref)
		fix := "use a Store/handle instead of raw pointers, or drop @hot"
		if !banned && noalloc {
			if r, ok := hotNoAllocBannedEffect(ref); ok {
				reason, banned = r, true
				fix = "preallocate outside the hot region (reserve), or use plain @hot which permits region allocation"
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
		if noalloc {
			label = "@hot(noalloc)"
		}
		a.errorf(fn.Pos(), "%s function %q %s (via the `%s` effect); %s", label, fn.Name, reason, key, fix)
	}
}
