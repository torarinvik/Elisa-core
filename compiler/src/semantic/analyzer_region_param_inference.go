package semantic

import (
	"elisacore/src/ast"
	"os"
	"strconv"
	"strings"
)

type regionForwardTarget struct {
	callee *ast.FuncDecl
	argPos int
}

type regionParamForwarding struct {
	direct map[string][]regionForwardTarget
	fields map[string][]regionForwardTarget
}

// containerGrowthMethods are the by-reference container operations that ALLOCATE into the
// container's backing region. A call to one of these on a parameter is what forces that
// parameter to carry a region: the push site needs an ambient arena, and across a function
// boundary the only one available is the caller's, threaded in. Read-only operations
// (`.count`, indexing, iteration) never need it, so a darray& param used purely for reading
// is deliberately NOT made region-polymorphic.
var containerGrowthMethods = map[string]bool{
	"push":    true,
	"extend":  true,
	"insert":  true,
	"resize":  true,
	"reserve": true,
	"append":  true,
	"put":     true, // dict
	"add":     true, // set
}

// inferRegionParamsForGrownContainerParams is the docs/75 S2 callee-side inference for
// cross-function lifetime. A function that GROWS a region-less by-reference container parameter
// — `def fill(out: mutable darray[T]&): out.push(...)` — is rewritten in place into the explicit
// region-parameter form `def fill[@__rg_out](out: mutable darray[T]& @__rg_out): ...`,
// so the rest of the pipeline sees exactly the (proven sound, S1) annotated shape: the
// caller's region arena is threaded as a hidden Arena& and resolves the growth allocator, and
// resolveType's region push-down unifies `@__rg_out` through the ref onto the container so it
// binds against the `&v` argument's region.
//
// This MUST run BEFORE collectValueSymbols so the synthesized region params and the stamped
// param `@r` are baked into the FuncType at construction time (RegionParams + the param's
// resolved container Region), keeping the AST and the type in lockstep without a second mutation.
//
// SAFETY: this adds no new lifetime power — it only spells, automatically, the `[@r]`/`@r`
// a user could write by hand (and which S1 proved sound under ASan). The "what is stored INTO
// the grown param" lifetime obligation stays guarded by the existing escape checker
// (checkNestedRegionStoreEscape et al.); inference only decides where the param's own backing
// arena comes from.
// regionBackingMemo keeps paramStructHasExplicitBackingIn's answer per resolved type.
type regionBackingMemo struct{ results map[Type]bool }

func (a *Analyzer) inferRegionParamsForGrownContainerParams(decls []scopedDecl) {
	// Struct field tables are complete and stay fixed for this pass.
	a.explicitBackingMemo = &regionBackingMemo{results: map[Type]bool{}}
	defer func() { a.explicitBackingMemo = nil }()
	cands, namespaceOf := collectRegionPolyCandidateFuncsScoped(decls)
	// This pass runs before collectValueSymbols, so funcDeclUsings has not been
	// populated yet. Keep the lexical resolution context alongside the function
	// declarations: the region-valued argument probe below may need to resolve a
	// callee's return annotation, and doing that in the caller's ambient context
	// makes a perfectly valid unqualified module-local type look unknown.
	funcScopes := map[*ast.FuncDecl]scopedDecl{}
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			funcScopes[n] = scoped
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					funcScopes[fn] = scoped
				}
			}
		}
	}
	// Resolve direct free-function call targets by name for the forwarding trigger. Overloads/last-wins
	// is acceptable: a wrong match only forgoes (or, at worst, conservatively adds) an inferred region
	// param — soundness is unaffected (the region threading is still verified at the real call site).
	// Program-lifetime region roots: a container growth lexically inside `in perm:` or
	// `in &<global>:` (a global/extern var arena) already has a program-lifetime allocation
	// region supplied — its backing outlives every possible caller storage, so the param needs
	// NO threaded region (and forcing one rejects the idiomatic helper that grows a caller-owned
	// container into a global perm arena). Scoped arenas (`region r:`, `with arena ... as s`)
	// are NOT here: their backing dies at scope exit, so growth into them must still thread (or
	// be rejected as a dangling write).
	permRoots := collectProgramLifetimeRoots(decls)
	funcByName := map[string]*ast.FuncDecl{}
	var inferable []*ast.FuncDecl
	for _, fn := range cands {
		if fn == nil {
			continue
		}
		funcByName[fn.Name] = fn
		// ALSO key a module member by the name a `::` call site actually writes. `Box::stow(bag, 12)`
		// parses to Ident{Name: "Box.stow"}, but flattening leaves the FuncDecl's Name bare ("stow"),
		// so keying by fn.Name alone made every module member invisible to the forwarding trigger:
		// the caller never learned it had to thread a region, and the call was then rejected with
		// "cannot infer region parameter" -- for a program the SAME module accepts when the sibling
		// is called unqualified, and that stage1 compiles and runs correctly.
		//
		// That gap is what blocked module blocks for anything with a growable API: every renderer in
		// nw-core takes `mutable DisplayList&` and forwards it onward, so `Display::rect(list, ...)`
		// was unreachable from any function that received `list` as a parameter.
		//
		// Both keys are registered, not one: the bare name is what an unqualified sibling call and
		// every top-level call site write, and dropping it would trade this bug for its mirror image.
		// The two can never collide -- an identifier cannot contain a dot, so a qualified key is
		// unreachable as a bare name.
		if qualified := joinQualifiedName(namespaceOf[fn], fn.Name); qualified != fn.Name {
			funcByName[qualified] = fn
		}
		// An Arena& parameter self-threads allocation and remains authoritative. Existing region
		// parameters, however, describe only the parameters explicitly tied to them; they must not
		// suppress inference for a separate region-less container/struct reference that is grown.
		if len(fn.Body) != 0 && !funcHasArenaParam(fn) {
			for _, param := range fn.Params {
				_, container := regionlessRefContainer(param.Type)
				_, structure := regionlessRefStruct(param.Type)
				if container || structure {
					inferable = append(inferable, fn)
					break
				}
			}
		}
	}
	for _, fn := range cands {
		if !hasCollectionAppendSyntax(fn) {
			continue
		}
		scoped := funcScopes[fn]
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			a.lowerCollectionAppends(fn)
		})
	}
	callers := make(map[*ast.FuncDecl][]*ast.FuncDecl)
	// Only functions with a stampable parameter can gain a region or need a
	// second look after a callee changes. All functions remain call targets.
	forwardingByFunc := collectRegionParamForwarding(inferable, funcByName, callers)
	// These facts depend only on a function body and parameter name. Bodies stop
	// changing after prepareCollectionAppends, while the worklist may revisit a
	// function after a callee gains a region parameter.
	bodyFacts := make(map[regionParamBodyFactsKey]*regionParamBodyFacts)
	// Worklist fixpoint. A function's verdict reads other functions only through names that
	// resolve in funcByName (forwarding targets, callee return types), so after the first full
	// pass only the callers of a function that just changed can gain a param (a recursive
	// function is its own caller). Each per-function pass is idempotent, so a function that
	// changed is complete until a callee changes. Rescanning every candidate per round cost ~0.6 s each on the seed.
	pending := inferable
	for len(pending) != 0 {
		queued := map[*ast.FuncDecl]bool{}
		var next []*ast.FuncDecl
		// A caller still ahead in this round sees the change when its turn comes.
		ahead := make(map[*ast.FuncDecl]int, len(pending))
		for i, fn := range pending {
			ahead[fn] = i
		}
		for i, fn := range pending {
			if !a.inferRegionParamsForGrownContainerParamsIn(fn, funcByName, permRoots, funcScopes, forwardingByFunc[fn], bodyFacts) {
				continue
			}
			for _, dep := range callers[fn] {
				if pos, ok := ahead[dep]; ok && pos > i {
					continue
				}
				if !queued[dep] {
					queued[dep] = true
					next = append(next, dep)
				}
			}
		}
		// Keep candidate order: stamping order names nothing, but deterministic rounds keep any
		// order-sensitive diagnostic stable.
		if len(next) > 1 {
			order := make(map[*ast.FuncDecl]int, len(next))
			for _, fn := range next {
				order[fn] = -1
			}
			kept := next[:0]
			for _, fn := range inferable {
				if _, ok := order[fn]; ok && order[fn] < 0 {
					order[fn] = 0
					kept = append(kept, fn)
				}
			}
			next = kept
		}
		pending = next
	}
	if os.Getenv("ELISA_REGION_FIXPOINT_SHADOW") != "" {
		for _, fn := range cands {
			if fn != nil && !funcHasArenaParam(fn) && a.inferRegionParamsForGrownContainerParamsIn(fn, funcByName, permRoots, funcScopes, forwardingByFunc[fn], bodyFacts) {
				panic("region inference worklist missed a dependency of " + fn.Name)
			}
		}
	}
	// Keep a call-site summary for region-stack assignment. The inference fixpoint above answers
	// whether a callee needs a region parameter; this dual summary records which explicit argument
	// positions receive values allocated in that region. It must be built after the fixpoint because
	// a callee can become region-polymorphic only after another function is stamped in an earlier
	// iteration.
	if a.regionParamCalleeParamIndex == nil {
		a.regionParamCalleeParamIndex = map[string]map[int]bool{}
	}
	for _, fn := range cands {
		if fn == nil || len(fn.RegionParams) == 0 {
			continue
		}
		params := a.readOnlyExpandedFuncDeclParams(fn)
		rps := map[string]bool{}
		for _, rp := range fn.RegionParams {
			rps[rp] = true
		}
		for i, param := range params {
			if !typeExprCarriesRegionParam(param.Type, rps) {
				continue
			}
			if a.regionParamCalleeParamIndex[fn.Name] == nil {
				a.regionParamCalleeParamIndex[fn.Name] = map[int]bool{}
			}
			a.regionParamCalleeParamIndex[fn.Name][i] = true
		}
	}
}

type regionParamBodyFactsKey struct {
	fn   *ast.FuncDecl
	name string
}

type regionParamBodyFacts struct {
	containerGrownKnown      bool
	containerGrown           bool
	containerReassignedKnown bool
	containerReassigned      bool
	fieldGrownKnown          bool
	fieldGrown               bool
	fieldAssignedFreshKnown  bool
	fieldAssignedFresh       bool
	storesBodyLocalKnown     bool
	storesBodyLocal          bool
}

func bodyFactsForParam(cache map[regionParamBodyFactsKey]*regionParamBodyFacts, fn *ast.FuncDecl, name string) *regionParamBodyFacts {
	key := regionParamBodyFactsKey{fn: fn, name: name}
	if facts := cache[key]; facts != nil {
		return facts
	}
	facts := &regionParamBodyFacts{}
	cache[key] = facts
	return facts
}

func (a *Analyzer) inferRegionParamsForGrownContainerParamsIn(fn *ast.FuncDecl, funcByName map[string]*ast.FuncDecl, permRoots map[string]bool, funcScopes map[*ast.FuncDecl]scopedDecl, forwarding regionParamForwarding, bodyFacts map[regionParamBodyFactsKey]*regionParamBodyFacts) bool {
	if fn == nil || len(fn.Body) == 0 {
		return false
	}
	changed := false
	inferred := map[string]string{} // param name -> inferred region-param name
	// ambientGrownParam is the container param (if any) grown by INSERTING region-allocated values
	// (the void-grower shape). Its inferred region becomes the function's ambient allocation region so
	// the inserted value's region-poly producer allocates into the caller's container region.
	ambientGrownParam := ""
	// Count regionless-ref-container params: the ambient-region binding is only SOUND when there is
	// exactly one. With two (`def bad(scratch: darray&, keep: darray&)`), binding the ambient region to
	// one container lets a value inserted into it ALSO be inserted into the other (a different-lifetime)
	// container — an escape the handle-level checker does not yet catch. Restricting to a single
	// container param removes that second target entirely (and covers every parser grower, all single
	// `out`). Other escape vectors (explicit inner regions, returns) remain covered by existing checks.
	// Only a MUTABLE container reference can receive a stored value; a read-only `darray[T]&`
	// is never a second target, so it does not count against the single-target restriction.
	containerParamCount := 0
	for i := range fn.Params {
		if _, ok := regionlessRefContainer(fn.Params[i].Type); ok {
			if _, mutable := fn.Params[i].Type.(*ast.MutableType); mutable {
				containerParamCount++
			}
		}
	}
	for i := range fn.Params {
		p := &fn.Params[i]
		var stamp *string
		if cstamp, ok := regionlessRefContainer(p.Type); ok {
			// A grown / literal-reassigned container ref param, OR one merely FORWARDED to a callee that
			// requires a region there (so the region must thread through this function too).
			grownWithRegionValue := a.containerParamGrownWithRegionValue(fn, p.Name, funcByName, funcScopes)
			facts := bodyFactsForParam(bodyFacts, fn, p.Name)
			if !facts.containerGrownKnown {
				facts.containerGrown = paramContainerIsGrownNeedingRegion(fn.Body, p.Name, permRoots)
				facts.containerGrownKnown = true
			}
			if !facts.containerReassignedKnown {
				facts.containerReassigned = paramContainerReassignedFromLiteral(fn.Body, p.Name)
				facts.containerReassignedKnown = true
			}
			if facts.containerGrown || facts.containerReassigned || a.forwardTargetsRequireRegion(forwarding.direct[p.Name]) || grownWithRegionValue {
				stamp = cstamp
				if grownWithRegionValue && ambientGrownParam == "" && containerParamCount == 1 {
					ambientGrownParam = p.Name
				}
			}
		} else if sstamp, ok := regionlessRefStruct(p.Type); ok {
			// docs/91 S4 Stage 1: a region-less by-ref STRUCT param whose CONTAINER FIELD is grown
			// (`def fill(m: mutable Mod&): m.bits.push(..)`), or which is forwarded to a region-
			// requiring callee. Stamp the inferred region onto the struct ref (`Mod& @r`): the
			// field-region propagation pushes it onto the resolved field container, and the same
			// return-escape checks that guard the explicit `[@r]` form cover every borrow-out vector
			// regardless of how the region param was introduced — no new lifetime power.
			facts := bodyFactsForParam(bodyFacts, fn, p.Name)
			if !facts.fieldAssignedFreshKnown {
				facts.fieldAssignedFresh = paramFieldAssignedFreshContainer(fn.Body, p.Name, funcByName)
				facts.fieldAssignedFreshKnown = true
			}
			if !facts.fieldGrownKnown {
				facts.fieldGrown = paramFieldContainerIsGrown(fn.Body, p.Name)
				facts.fieldGrownKnown = true
			}
			assignedFresh := facts.fieldAssignedFresh
			if !assignedFresh && !facts.fieldGrown &&
				!a.forwardTargetsRequireRegion(forwarding.fields[p.Name]) &&
				!a.forwardTargetsRequireRegion(forwarding.direct[p.Name]) {
				continue
			}
			// The backing type matters only if the body would infer a region.
			scoped := funcScopes[fn]
			if a.paramStructIsProgramLivedIn(scoped, p.Type) ||
				a.paramStructHasExplicitBackingIn(scoped, fn, p.Type) {
				continue
			}
			stamp = sstamp
			// A body-built value stored into the sole writable struct target adopts the same
			// way, but only onto a region this parameter already threads.
			if !assignedFresh && containerParamCount == 0 && paramIsWritableStructRef(p) && cachedParamStoresBodyLocal(facts, fn, p.Name) && !a.hasOtherFreshFieldTarget(fn, p.Name) {
				assignedFresh = true
			}
			// The fresh backing stored into the field is allocated by this body, so the
			// function's ambient region must be the caller's (the field's) region. Same
			// single-target restriction as the container grower: with any other region-less
			// container param, a value could be routed into a different-lifetime target.
			if assignedFresh && ambientGrownParam == "" && containerParamCount == 0 && !a.hasOtherFreshFieldTarget(fn, p.Name) {
				ambientGrownParam = p.Name
			}
		}
		if stamp == nil {
			continue
		}
		// Param names are unique within a function, so the per-param suffix yields a unique,
		// deterministic region-param name (no Date/random — must stay resume-stable).
		name := inferredRegionParamName(fn, p.Name)
		*stamp = name
		fn.RegionParams = append(fn.RegionParams, name)
		fn.InferredRegionParams = append(fn.InferredRegionParams, name)
		inferred[p.Name] = name
		changed = true
	}
	if len(inferred) > 0 {
		inferReturnViewRegion(fn, inferred)
	}
	// Record the ambient grown-container region on the FuncType so the backend binds this function's
	// ambient allocation region to it (the void-grower fix): inserted region-poly values then land in
	// the caller-owned container's region instead of a per-call arena freed on return.
	if ambientGrownParam != "" {
		// Recorded on the AST FuncDecl (like RegionParams) — symbol/FuncType resolution is not reliably
		// available at this pre-pass, and the backend reads the FuncDecl directly.
		fn.AmbientGrownContainerRegion = inferred[ambientGrownParam]
		// Callers' stack assignment must know which arg position feeds the ambient region: a
		// container forwarded here gets adopted payload allocations interleaved into its arena,
		// so its stack cannot be tail-only reserve_commit (see assignRegionStacks).
		if a.ambientGrownCalleeParamIndex != nil {
			for i := range fn.Params {
				if fn.Params[i].Name == ambientGrownParam {
					a.ambientGrownCalleeParamIndex[fn.Name] = i
					break
				}
			}
		}
	}
	return changed
}

func cachedParamStoresBodyLocal(facts *regionParamBodyFacts, fn *ast.FuncDecl, name string) bool {
	if !facts.storesBodyLocalKnown {
		facts.storesBodyLocal = paramStoresBodyLocal(fn, name)
		facts.storesBodyLocalKnown = true
	}
	return facts.storesBodyLocal
}

// inferredRegionParamName reserves a deterministic region binder that cannot
// shadow a source-declared generic parameter. Region names participate in
// specialization bindings, so a synthesized __rg_* name must be distinct from
// every declared type, region, permission, value, or state generic parameter.
func inferredRegionParamName(fn *ast.FuncDecl, parameter string) string {
	base := "__rg_" + parameter
	used := make(map[string]bool, len(fn.TypeParams)+len(fn.RegionParams)+len(fn.PermissionParams)+len(fn.GenericParams))
	for _, name := range fn.TypeParams {
		used[name] = true
	}
	for _, name := range fn.RegionParams {
		used[name] = true
	}
	for _, name := range fn.PermissionParams {
		used[name] = true
	}
	for _, param := range fn.GenericParams {
		used[param.Name] = true
	}
	name := base
	for suffix := 1; used[name]; suffix++ {
		name = base + "_" + strconv.Itoa(suffix)
	}
	return name
}

// calleeReturnTypeIsRegionValued resolves a callee's declared return type for the
// region-valued PROBE: in the callee's own namespace, and without reporting.
//
// Both halves matter, and both only became reachable when module members started
// resolving in funcByName at all:
//
//   - A module member's return type is written BARE inside its module
//     (`def spacer() -> Row` in `module M:`). Resolving it from the CALLER's namespace
//     cannot see `M.Row`.
//
//   - resolveType REPORTS. So a probe that only asks "is this region-valued?" emitted a
//     real "unknown type Row" error, pinned at the callee's own signature, and failed
//     the compile — for this, which the pre-fix compiler accepted:
//
//     def fill(rows: mutable darray[M::Row]&):
//     rows.push(M::spacer())
//
//     Note the shape: it needs a darray PARAMETER. Pushing onto a darray LOCAL, or
//     binding the result to a local, never reaches this probe.
//
// A probe must not be able to fail a compile on its own, so the suppression is not
// merely belt-and-braces: any future type this cannot resolve should make the probe
// answer "no", not report.
func (a *Analyzer) calleeReturnTypeIsRegionValued(qualifiedName string, callee *ast.FuncDecl) bool {
	savedNamespace := a.currentNamespace
	savedSuppress := a.suppressDiagnostics
	if idx := strings.LastIndex(qualifiedName, "."); idx >= 0 {
		a.currentNamespace = qualifiedName[:idx]
	}
	restoreReportProbe1 := a.beginProofReportProbe()
	defer restoreReportProbe1.restore()
	a.suppressDiagnostics = true
	resolved := a.resolveType(callee.ReturnType)
	a.suppressDiagnostics = savedSuppress
	restoreReportProbe1.restore()
	a.currentNamespace = savedNamespace
	return a.typeIsRegionValued(resolved)
}
