package semantic

import (
	"reflect"

	"elisacore/src/ast"
)

// callDisjointObservation is one observed call site of a candidate kernel: the proven-distinct
// container-param pairs and the per-param self-noalias bits computed for THAT call. The
// whole-program fact (FuncDisjointParamInfo) is the intersection of these across every call.
type callDisjointObservation struct {
	containerParams []int
	distinctPairs   map[[2]int]bool
	selfNoalias     map[int]bool
}

// registerCallDisjointObservation records one call site against its resolved callee declaration,
// for the per-callee aggregation in finalizeFuncDisjointParams. It runs for every analyzed call
// whose callee resolves to a direct, by-name free function with >=2 container-ref params —
// including calls where no pair is distinct, since those must still pull pairs OUT of the
// cross-site intersection. Calls whose callee cannot be resolved by name are simply not recorded;
// finalize separately disqualifies any callee reachable through an unobserved path (address-taken
// / overloaded), so an unrecorded call can never leave a stale over-broad fact.
func (a *Analyzer) registerCallDisjointObservation(call *ast.CallExpr, containerParams []int, distinctPairs map[[2]int]bool, selfNoalias map[int]bool) {
	if a == nil || a.disjointCallSites == nil || len(containerParams) < 2 {
		return
	}
	decl, ok := a.resolveDirectCallFuncDecl(call)
	if !ok {
		return
	}
	a.disjointCallSites[decl] = append(a.disjointCallSites[decl], callDisjointObservation{
		containerParams: append([]int(nil), containerParams...),
		distinctPairs:   distinctPairs,
		selfNoalias:     selfNoalias,
	})
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

// finalizeFuncDisjointParams aggregates the per-call proven_distinct observations into the
// whole-program, per-callee FuncDisjointParams fact (docs/84 §3.2, Increment 3a). A param pair is
// kept only when proven distinct at EVERY observed call site (set intersection); a self-noalias
// bit only when set at every site. Eligibility is gated to free functions that are (a) uniquely
// named and (b) never address-taken, so every call is an observed direct by-name call and the
// intersection is complete — see the FuncDisjointParamInfo soundness note.
func (a *Analyzer) finalizeFuncDisjointParams(decls []scopedDecl) {
	if a == nil || len(a.disjointCallSites) == 0 {
		return
	}
	nameCounts, valueUsedNames := scanFuncNameUsage(decls)

	result := map[*ast.FuncDecl]*FuncDisjointParamInfo{}
	for decl, observations := range a.disjointCallSites {
		if decl == nil || len(observations) == 0 {
			continue
		}
		// Unique name: a duplicated name means by-name resolution at a call site may bind a
		// different overload than this decl, so observed sites cannot be trusted as complete.
		if nameCounts[decl.Name] != 1 {
			continue
		}
		// Address-taken: any use of the name as a value (not a direct call target) means the
		// function could be invoked from an unobserved indirect site.
		if valueUsedNames[decl.Name] {
			continue
		}
		distinct, selfNoalias := intersectDisjointObservations(observations)
		if len(distinct) == 0 {
			continue
		}
		result[decl] = &FuncDisjointParamInfo{DistinctPairs: distinct, SelfNoalias: selfNoalias}
	}
	if len(result) != 0 {
		a.funcDisjointParams = result
	}
}

// intersectDisjointObservations keeps only the pairs / self-bits proven at every observation.
func intersectDisjointObservations(observations []callDisjointObservation) (map[[2]int]bool, map[int]bool) {
	distinct := map[[2]int]bool{}
	selfNoalias := map[int]bool{}
	// Seed from the first observation, then intersect with the rest.
	for pair := range observations[0].distinctPairs {
		distinct[pair] = true
	}
	for idx := range observations[0].selfNoalias {
		selfNoalias[idx] = true
	}
	for _, obs := range observations[1:] {
		for pair := range distinct {
			if !obs.distinctPairs[pair] {
				delete(distinct, pair)
			}
		}
		for idx := range selfNoalias {
			if !obs.selfNoalias[idx] {
				delete(selfNoalias, idx)
			}
		}
	}
	return distinct, selfNoalias
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
