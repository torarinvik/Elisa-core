package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// pairDisjointEvidence is why a callee parameter pair could be distinct AT ONE call site:
// either proven outright by the per-call fresh-anchor predicate (`base`), or CONDITIONAL on the
// enclosing function's own parameters being disjoint (`depend` — both args were the enclosing
// function's params depP/depQ, so the pair is distinct here iff the enclosing function's
// (depP,depQ) is itself proven). Absence of evidence for a pair means "not distinct here", which
// removes the pair from the callee's whole-program fact.
type pairDisjointEvidence struct {
	base   bool
	depend bool
	depP   int
	depQ   int
}

// callDisjointObservation is one observed call site of a candidate kernel: the call's enclosing
// function, the callee's container-param indices, and the per-pair evidence. The whole-program
// fact (FuncDisjointParamInfo) is derived by the least-fixpoint in finalizeFuncDisjointParams.
type callDisjointObservation struct {
	enclosing       *ast.FuncDecl
	containerParams []int
	evidence        map[[2]int]pairDisjointEvidence
}

// registerCallDisjointObservation records one call site against its resolved callee declaration,
// for the per-callee fixpoint in finalizeFuncDisjointParams. It runs for every analyzed call whose
// callee resolves to a direct, by-name free function with >=2 container-ref params — including
// calls where no pair is distinct, since those must still keep a pair OUT of the callee's fact.
// Calls whose callee cannot be resolved by name are simply not recorded; finalize separately
// disqualifies any callee reachable through an unobserved path (address-taken / overloaded), so an
// unrecorded call can never leave a stale over-broad fact.
func (a *Analyzer) registerCallDisjointObservation(call *ast.CallExpr, containerParams []int, evidence map[[2]int]pairDisjointEvidence) {
	if a == nil || a.disjointCallSites == nil || len(containerParams) < 2 {
		return
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		return
	}
	a.disjointCallSites[decl] = append(a.disjointCallSites[decl], callDisjointObservation{
		enclosing:       a.currentFuncDecl,
		containerParams: append([]int(nil), containerParams...),
		evidence:        evidence,
	})
}

// orderedPair returns {i,j} with i<j, the canonical key used throughout the disjointness maps.
func orderedPair(i, j int) [2]int {
	if i > j {
		i, j = j, i
	}
	return [2]int{i, j}
}

// resolveDirectCallFuncDecl returns the function declaration a call targets, but ONLY for the
// unambiguous direct-by-name form `f(...)` where `f` is a plain identifier resolving to a
// FuncDecl symbol. Method/field calls, indirect calls through function values, and specialized
// generic targets all return false: their call sites are not guaranteed to be fully observable
// by name, so the conservative choice is to not record them (and finalize excludes such callees).
func (a *Analyzer) resolveDirectCallFuncDecl(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	if call == nil || a.currentScope == nil {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	sym, ok := a.currentScope.Lookup(ident.Name)
	if !ok || sym == nil {
		return nil, false
	}
	decl, ok := sym.Node.(*ast.FuncDecl)
	if !ok || decl == nil {
		return nil, false
	}
	return decl, true
}

// finalizeFuncDisjointParams derives the whole-program, per-callee FuncDisjointParams fact from
// the observed call sites by a LEAST-fixpoint over the call graph (docs/84 §3.2 + the forwarding
// extension). A callee parameter pair is proven distinct only when EVERY observed call site of the
// callee contributes distinctness — where a site contributes either by direct fresh-anchor
// evidence (`base`) or because it forwards the enclosing function's own params and that enclosing
// pair is ITSELF already proven (`depend`).
//
// Least-fixpoint (start with nothing proven, add only when grounded) is the soundness crux: a
// purely forwarding cycle with no fresh-anchored base never becomes proven, because its only
// evidence is a `depend` that is never discharged. A greatest-fixpoint (start all true, remove on
// contradiction) would leave such ungrounded cycles unsoundly true. Conservative by construction:
// it can only fail to prove a true fact (lost optimization), never prove a false one.
//
// Eligibility (unique name, never address-taken) is required for a function to (a) be stampable
// and (b) have its facts TRUSTED as a `depend` target — both need every call site observed, which
// only holds for a direct-by-name, non-address-taken free function. See the soundness note on
// FuncDisjointParamInfo.
func (a *Analyzer) finalizeFuncDisjointParams(decls []scopedDecl) {
	if a == nil || len(a.disjointCallSites) == 0 {
		return
	}
	nameCounts, valueUsedNames := scanFuncNameUsage(decls)
	eligible := func(fn *ast.FuncDecl) bool {
		return fn != nil && nameCounts[fn.Name] == 1 && !valueUsedNames[fn.Name]
	}

	// proven[f] = the param pairs proven distinct so far. Only ever populated for eligible f, so a
	// `depend` on f's fact (queried below) implicitly requires f eligible.
	proven := map[*ast.FuncDecl]map[[2]int]bool{}
	contributes := func(obs callDisjointObservation, pair [2]int) bool {
		ev, ok := obs.evidence[pair]
		if !ok {
			return false
		}
		if ev.base {
			return true
		}
		if ev.depend {
			if pr, ok := proven[obs.enclosing]; ok && pr[[2]int{ev.depP, ev.depQ}] {
				return true
			}
		}
		return false
	}

	for changed := true; changed; {
		changed = false
		for decl, observations := range a.disjointCallSites {
			if !eligible(decl) || len(observations) == 0 {
				continue
			}
			// Candidate pairs = those with evidence at the first site; a pair must contribute at
			// EVERY site to be proven, so it must appear here. (Pairs absent from site 0 can never
			// be proven.)
			for pair := range observations[0].evidence {
				if proven[decl][pair] {
					continue
				}
				allContribute := true
				for _, obs := range observations {
					if !contributes(obs, pair) {
						allContribute = false
						break
					}
				}
				if allContribute {
					if proven[decl] == nil {
						proven[decl] = map[[2]int]bool{}
					}
					proven[decl][pair] = true
					changed = true
				}
			}
		}
	}

	result := map[*ast.FuncDecl]*FuncDisjointParamInfo{}
	for decl, pairs := range proven {
		if len(pairs) == 0 {
			continue
		}
		containerParams := a.disjointCallSites[decl][0].containerParams
		result[decl] = &FuncDisjointParamInfo{
			DistinctPairs: pairs,
			SelfNoalias:   selfNoaliasFromPairs(containerParams, pairs),
		}
	}
	if len(result) != 0 {
		a.funcDisjointParams = result
	}
}

// selfNoaliasFromPairs marks param i self-noalias when it is proven distinct from EVERY other
// container param — the precondition for the backend's full memcheck-eliding stamp.
func selfNoaliasFromPairs(containerParams []int, pairs map[[2]int]bool) map[int]bool {
	self := map[int]bool{}
	if len(containerParams) < 2 {
		return self
	}
	for _, i := range containerParams {
		all := true
		for _, j := range containerParams {
			if i == j {
				continue
			}
			if !pairs[orderedPair(i, j)] {
				all = false
				break
			}
		}
		if all {
			self[i] = true
		}
	}
	return self
}

// scanFuncNameUsage walks every declaration and returns (1) how many top-level FuncDecls carry
// each name and (2) the set of identifier names that appear as a VALUE — anywhere other than the
// direct `f` in a `f(...)` call. The second set conservatively over-approximates address-taken
// functions: a name shadowed by an unrelated local merely disqualifies a same-named kernel
// (lost optimization, never a miscompile).
func scanFuncNameUsage(decls []scopedDecl) (nameCounts map[string]int, valueUsedNames map[string]bool) {
	nameCounts = map[string]int{}
	valueUsedNames = map[string]bool{}
	for _, scoped := range decls {
		if fn, ok := scoped.Decl.(*ast.FuncDecl); ok && fn != nil {
			nameCounts[fn.Name]++
		}
		scanValueUsedIdents(reflect.ValueOf(scoped.Decl), valueUsedNames)
	}
	return nameCounts, valueUsedNames
}

// scanValueUsedIdents reflect-walks an AST node, adding every identifier reached in a value
// position to `names`. The sole exemption is the direct callee of a call: for a *ast.CallExpr
// whose Func is a plain *ast.Ident, that identifier is a call target, not a function value, so it
// is skipped while the rest of the call (args, receiver, a non-Ident Func) is still walked.
func scanValueUsedIdents(v reflect.Value, names map[string]bool) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		scanValueUsedIdents(v.Elem(), names)
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		switch n := v.Interface().(type) {
		case *ast.Ident:
			names[n.Name] = true
			return
		case *ast.CallExpr:
			// Skip a direct-Ident callee (a call target, not a value); walk everything else.
			if _, direct := n.Func.(*ast.Ident); !direct {
				scanValueUsedIdents(reflect.ValueOf(n.Func), names)
			}
			rt := v.Elem()
			for i := 0; i < rt.NumField(); i++ {
				if rt.Type().Field(i).Name == "Func" {
					continue
				}
				scanValueUsedIdents(rt.Field(i), names)
			}
			return
		}
		scanValueUsedIdents(v.Elem(), names)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			scanValueUsedIdents(v.Field(i), names)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			scanValueUsedIdents(v.Index(i), names)
		}
	}
}
