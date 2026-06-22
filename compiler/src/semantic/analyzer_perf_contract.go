package semantic

import (
	"elisacore/src/ast"
)

// Enforceable performance contracts (docs/70) — the perf analog of `-strict`. These promote the
// advisory `-Wperf` machinery (and the `@hot` / `NoBoundsChecks` analyses) into HARD-ERROR
// contracts on the annotated function, so a refactor that reintroduces a heap allocation, a runtime
// bounds check, or a lock on a contracted path fails to compile.
//
// All three reuse already-finalized analysis state computed for the function body, so they add no
// new analysis — only promotion:
//
//   - @noalloc  — no heap allocation reachable (transitively). Region/stack/arena allocation is
//                 allowed (that is the point: fast + safe). Enforced against the function's
//                 transitive permission set (`Memory.Allocate`), which already folds in the whole
//                 statically-resolvable call tree. An unanalyzable indirect / unknown call whose
//                 allocation behaviour cannot be ruled out also fails the contract (sound: we reject
//                 rather than vacuously pass).
//   - @inbounds — every array/darray/view index in the function is statically proven in-bounds; any
//                 index that would emit a runtime bounds check is an error (reuses the
//                 NoBoundsChecks audit's currentFunctionGuardedIndexes).
//   - @nolock   — no lock acquire / condition-wait / blocking-wait reachable (transitive permission
//                 set), plus the same unanalyzable-call rejection.
//
// SOUNDNESS: PermissionRefs is the transitive effect set the entire effect system trusts; the
// guarded-index list counts every access that would emit a check. Both over-report at worst (a safe
// false-positive), never under-report, so a contract can never be vacuously satisfied.

// perfContractUnknownCallEffect reports an effect that represents a call the analyzer cannot see
// through (an indirect call or an unknown/opaque extern call). For an allocation- or lock-free
// contract such a call cannot be ruled out, so it must fail the contract.
func perfContractUnknownCallEffect(ref ast.PermissionRef) (string, bool) {
	switch {
	case ref.Name == "Unsafe" && ref.Member == "IndirectCall":
		return "performs an indirect call the analyzer cannot prove is contract-clean", true
	case ref.Name == "Blocking" && ref.Member == "UnknownCall":
		return "calls an opaque function the analyzer cannot prove is contract-clean", true
	}
	return "", false
}

// noallocBannedEffect reports the allocation/free effects a @noalloc function must not reach.
func noallocBannedEffect(ref ast.PermissionRef) (string, bool) {
	if ref.Name == "Memory" && (ref.Member == "Allocate" || ref.Member == "Release") {
		return "allocates or frees on the heap", true
	}
	return perfContractUnknownCallEffect(ref)
}

// nolockBannedEffect reports the lock / blocking-wait effects a @nolock function must not reach.
func nolockBannedEffect(ref ast.PermissionRef) (string, bool) {
	switch ref.Name {
	case "Sync":
		switch ref.Member {
		case "Lock", "Unlock", "Wait", "Notify":
			return "acquires a lock or waits on a condition", true
		}
	case "Blocking":
		switch ref.Member {
		case "Lock", "Wait", "Join":
			return "blocks on a lock, wait, or join", true
		}
	case "Pool":
		switch ref.Member {
		case "Await", "WaitAll":
			return "blocks awaiting pool work", true
		}
	case "Thread":
		if ref.Member == "Join" {
			return "blocks joining a thread", true
		}
	}
	return perfContractUnknownCallEffect(ref)
}

// checkPerfContracts enforces @noalloc / @inbounds / @nolock against a function's finalized
// (transitive) permission set and statically-proven-index audit. Called from the same site as
// checkHotContract, where fnType.PermissionRefs and currentFunctionGuardedIndexes are final.
func (a *Analyzer) checkPerfContracts(fn *ast.FuncDecl, fnType *FuncType) {
	if fn == nil || fnType == nil {
		return
	}
	if funcHasAnnotation(fn, "noalloc") {
		a.enforcePerfEffectContract(fn, fnType, "@noalloc", noallocBannedEffect,
			"allocate from a region/arena/stack instead (e.g. `new[auto] T(...)`, or preallocate with `reserve`), or drop @noalloc")
	}
	if funcHasAnnotation(fn, "nolock") {
		a.enforcePerfEffectContract(fn, fnType, "@nolock", nolockBannedEffect,
			"restructure to avoid the lock/blocking wait on this path, or drop @nolock")
	}
	if funcHasAnnotation(fn, "inbounds") {
		if len(a.currentFunctionGuardedIndexes) > 0 {
			a.errorf(a.currentFunctionGuardedIndexes[0], "@inbounds function %q has an index access that is not statically proven in-bounds, so a runtime bounds check would be emitted; prove the index (iterate with `for x in coll`, or guard with a bound the prover understands, e.g. `if i < coll.count:` or a `requires` refinement)", fn.Name)
		}
	}
}

// enforcePerfEffectContract reports the first banned effect (per category) reachable from a
// contracted function, naming the offending site precisely (the effect's recorded position).
func (a *Analyzer) enforcePerfEffectContract(fn *ast.FuncDecl, fnType *FuncType, label string, banned func(ast.PermissionRef) (string, bool), fix string) {
	seen := map[string]bool{}
	for _, ref := range fnType.PermissionRefs {
		reason, isBanned := banned(ref)
		if !isBanned {
			continue
		}
		key := ref.Name + "." + ref.Member
		if seen[key] {
			continue
		}
		seen[key] = true
		pos := ref.Position
		if pos.File == "" {
			pos = fn.Pos()
		}
		a.errorf(pos, "%s function %q %s (via the `%s` effect); %s", label, fn.Name, reason, key, fix)
	}
}
