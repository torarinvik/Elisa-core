package semantic

import (
	"reflect"
	"strings"

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
	decl, ok := a.resolveDirectCallFuncDeclForReport(call)
	if a != nil {
		a.addProofReportOrigin(decl)
	}
	return decl, ok
}

func (a *Analyzer) resolveDirectCallFuncDeclForReport(call *ast.CallExpr) (*ast.FuncDecl, bool) {
	if call == nil {
		return nil, false
	}
	// Declaration-level validation (for example `validateGhostFuncDecl`) runs outside any function
	// body, so there is no current scope. Calls there can only name program-level functions, which
	// the global scope resolves exactly; a function body always resolves through its own scope.
	scope := a.currentScope
	if scope == nil {
		scope = a.globalScope
	}
	if scope == nil {
		return nil, false
	}
	ident, ok := call.Func.(*ast.Ident)
	if !ok || ident == nil {
		return nil, false
	}
	sym, ok := scope.Lookup(ident.Name)
	if !ok || sym == nil {
		// No binding of the spelled name: resolve a module-scoped or `::`-qualified function the
		// way expression analysis does (namespace, using and import candidates, with visibility).
		// Only a function symbol is accepted, and only when nothing in scope binds the name.
		if a.globalScope == nil {
			return nil, false
		}
		global, _, found := a.lookupVisibleGlobal(ident.Name)
		if !found || global == nil || global.Kind != SymbolFunc {
			return nil, false
		}
		// Only a plain function declared under the spelled name: an overload or extension set is
		// registered under a mangled name and is resolved by argument types, never by name here.
		fd, isDecl := global.Node.(*ast.FuncDecl)
		if !isDecl || fd == nil || unqualifiedCallName(fd.Name) != unqualifiedCallName(ident.Name) {
			return nil, false
		}
		sym = global
	}
	// Parameters can carry their enclosing FuncDecl as source metadata.
	// That node does not turn an indirect callback into a direct function.
	if sym.Kind != SymbolFunc {
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
func (a *Analyzer) finalizeFuncDisjointParamsFull(decls []scopedDecl) {
	if a == nil {
		return
	}
	if len(a.disjointCallSites) == 0 {
		a.funcDisjointParams = map[*ast.FuncDecl]*FuncDisjointParamInfo{}
		return
	}
	nameCounts, valueUsedNames := scanFuncNameUsage(decls)
	a.finalizeFuncDisjointParamsFullWithNameUsage(decls, nameCounts, valueUsedNames)
}

func (a *Analyzer) finalizeFuncDisjointParamsFullWithNameUsage(decls []scopedDecl, nameCounts map[string]int, valueUsedNames map[string]bool) {
	if a == nil {
		return
	}
	if len(a.disjointCallSites) == 0 {
		a.funcDisjointParams = map[*ast.FuncDecl]*FuncDisjointParamInfo{}
		return
	}
	headerStable := map[*ast.FuncDecl]bool{}
	for fn := range a.disjointCallSites {
		if fn != nil && nameCounts[fn.Name] == 1 && !valueUsedNames[fn.Name] {
			headerStable[fn] = a.containerHeadersStable(fn)
		}
	}
	eligible := func(fn *ast.FuncDecl) bool { return headerStable[fn] }

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
	a.funcDisjointParams = result
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
	return scanFuncNameUsageWithMemo(decls, newScanValueUsedIdentMemo())
}

// scanFuncNameUsageWithMemo shares one traversal memo across all declaration roots. The
// analyzed AST may share child nodes through `Args`/`ResolvedArgs` and through lowered
// expressions; resetting the visited set for each root makes the same DAG expensive again.
func scanFuncNameUsageWithMemo(decls []scopedDecl, memo *scanValueUsedIdentMemo) (nameCounts map[string]int, valueUsedNames map[string]bool) {
	if memo == nil {
		memo = newScanValueUsedIdentMemo()
	}
	nameCounts = map[string]int{}
	valueUsedNames = map[string]bool{}
	for _, scoped := range decls {
		if fn, ok := scoped.Decl.(*ast.FuncDecl); ok && fn != nil {
			nameCounts[fn.Name]++
		}
		scanValueUsedIdentsWithMemo(reflect.ValueOf(scoped.Decl), valueUsedNames, memo)
	}
	return nameCounts, valueUsedNames
}

type scanValueIdentVisit struct {
	typ reflect.Type
	ptr uintptr
}

type scanValueUsedIdentMemo struct {
	seen             map[scanValueIdentVisit]struct{}
	callFieldIndexes map[reflect.Type][]int
}

func newScanValueUsedIdentMemo() *scanValueUsedIdentMemo {
	return &scanValueUsedIdentMemo{
		seen:             map[scanValueIdentVisit]struct{}{},
		callFieldIndexes: map[reflect.Type][]int{},
	}
}

// callFields returns every CallExpr field except Func. The caller scans a non-ident Func
// separately because it is a function value; a direct *ast.Ident Func remains exempt. Cache
// the complete field inventory by type so the specialized walk cannot accidentally omit
// ResolvedArgs, implicit args, or future AST fields.
func (memo *scanValueUsedIdentMemo) callFields(t reflect.Type) []int {
	if fields, ok := memo.callFieldIndexes[t]; ok {
		return fields
	}
	all := reflectFields(t)
	indexes := make([]int, 0, len(all))
	for i := range all {
		if all[i].Name != "Func" {
			indexes = append(indexes, i)
		}
	}
	memo.callFieldIndexes[t] = indexes
	return indexes
}

// scanValueUsedIdents reflect-walks an AST node, adding every identifier reached in a value
// position to `names`. The sole exemption is the direct callee of a call: for a *ast.CallExpr
// whose Func is a plain *ast.Ident, that identifier is a call target, not a function value, so it
// is skipped while the rest of the call (args, receiver, a non-Ident Func) is still walked.
func scanValueUsedIdents(v reflect.Value, names map[string]bool) {
	scanValueUsedIdentsWithMemo(v, names, newScanValueUsedIdentMemo())
}

func scanValueUsedIdentsWithMemo(v reflect.Value, names map[string]bool, memo *scanValueUsedIdentMemo) {
	if !v.IsValid() {
		return
	}
	if memo == nil {
		memo = newScanValueUsedIdentMemo()
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		scanValueUsedIdentsWithMemo(v.Elem(), names, memo)
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		visit := scanValueIdentVisit{typ: v.Type(), ptr: v.Pointer()}
		if _, ok := memo.seen[visit]; ok {
			return
		}
		memo.seen[visit] = struct{}{}
		switch n := v.Interface().(type) {
		case *ast.Ident:
			names[n.Name] = true
			return
		case *ast.CallExpr:
			// A skipped direct callee is intentionally not visited/memoized here: the
			// same Ident pointer may occur later as an actual function value.
			if _, direct := n.Func.(*ast.Ident); !direct {
				scanValueUsedIdentsWithMemo(reflect.ValueOf(n.Func), names, memo)
			}
			rt := v.Elem()
			for _, index := range memo.callFields(rt.Type()) {
				scanValueUsedIdentsWithMemo(rt.Field(index), names, memo)
			}
			return
		}
		scanValueUsedIdentsWithMemo(v.Elem(), names, memo)
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			scanValueUsedIdentsWithMemo(v.Field(i), names, memo)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			scanValueUsedIdentsWithMemo(v.Index(i), names, memo)
		}
	}
}

// unqualifiedCallName strips a `::` or `.` module qualification from a callee spelling.
func unqualifiedCallName(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}
