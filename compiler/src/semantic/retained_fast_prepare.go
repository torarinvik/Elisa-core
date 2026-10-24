package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"elisacore/src/ast"
	"elisacore/src/unparse"
)

// Fast preparation of an edited body (docs: scratchpad PLAN-incremental-decl.md).
//
// An update takes each edited function's body in the form the declaration phase leaves it
// in, from a full declaration phase run over a fresh parse (the scratch). For a body-only
// edit whose declaration-level results are unchanged, that form is the fresh body with the
// phase's body rewrites applied against the program-wide results the live analysis already
// holds. ELISACORE_RETAINED_FAST_PREPARE=shadow computes it beside the scratch's and reports
// every difference; =on uses it for body-only edits (fastPathBodies).

var retainedFastPrepareMode = os.Getenv("ELISACORE_RETAINED_FAST_PREPARE")

// retainedClassifyState is what the region-polymorphism classification decided program-wide
// and does not keep on the analyzer: the transitive store needs and the region-backed enums.
type retainedClassifyState struct {
	storeNeeds   map[*FuncType]map[string]*EnumType
	regionBacked map[string]bool
	exportTarget map[string]bool
}

// retainedAppendOnlyState is checkAppendOnlyStores' program-wide facts; they read only
// declaration headers, so a body edit can re-check its function against them.
type retainedAppendOnlyState struct {
	facts  *aoFacts
	owners map[string]string
}

// appendOnlyReports reports whether the append-only store check flags edit. It records
// nothing: a flagged edit goes through the scratch, which reports it.
func (r *RetainedAnalysis) appendOnlyReports(a *Analyzer, edit *ast.FuncDecl, sd scopedDecl) bool {
	ao := r.appendOnly
	if ao == nil {
		return true
	}
	if len(ao.facts.stores) == 0 {
		return false
	}
	seen, n := a.reportedDiagnostics, len(a.diagnostics)
	a.reportedDiagnostics = nil
	a.withResolutionContext(sd.Namespace, sd.Usings, func() {
		a.aoCheckFunc(ao.facts, sd.Namespace, ao.owners[sd.Namespace], edit)
	})
	reported := len(a.diagnostics) != n
	a.diagnostics, a.reportedDiagnostics = a.diagnostics[:n], seen
	return reported
}

// classifierFoldShape is foldableClassifierTable's syntactic precondition: such a body may be
// replaced by a synthesized table, which the fast path does not replay.
func classifierFoldShape(fn *ast.FuncDecl) bool {
	if len(fn.Params) != 1 || !isCharType(fn.Params[0].Type) || len(fn.Body) != 1 {
		return false
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		return false
	}
	_, ok = ret.Value.(*ast.MatchExpr)
	return ok
}

// fastPrepareBody returns live's body as the declaration phase would prepare edit's, or a
// reason it cannot. edit is not modified.
func (r *RetainedAnalysis) fastPrepareBody(a *Analyzer, live, edit *ast.FuncDecl, sd scopedDecl) ([]ast.Stmt, string) {
	cs := r.classify
	if cs == nil {
		return nil, "no classification state"
	}
	sym, ok := a.funcDeclSymbols[live]
	if !ok || sym == nil {
		return nil, "no symbol"
	}
	ft, _ := sym.Type.(*FuncType)
	if ft == nil {
		return nil, "no function type"
	}
	body, ok := ast.CloneBlock(edit.Body)
	if !ok {
		snap, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{edit}}, 1<<30)
		if err != nil {
			return nil, "body does not snapshot: " + err.Error()
		}
		copied, err := snap.CopyDeclaration(0)
		copiedFn, isFn := copied.(*ast.FuncDecl)
		if err != nil || !isFn {
			return nil, "body does not copy"
		}
		body = copiedFn.Body
	}
	fn := *edit
	fn.Body = body
	// Header-derived state is the live function's; only the body is new.
	fn.Params, fn.RegionParams, fn.InferredRegionParams = live.Params, live.RegionParams, live.InferredRegionParams
	fn.AmbientGrownContainerRegion = live.AmbientGrownContainerRegion
	a.funcDeclSymbols[&fn] = sym
	defer delete(a.funcDeclSymbols, &fn)
	why := ""
	a.withResolutionContext(sd.Namespace, sd.Usings, func() {
		a.prepareCollectionAppends(&fn)
		// classifyRegionPolymorphicFunctions, in its order, for one function.
		if ft.RegionPolymorphic && len(fn.Body) != 0 && bodyAllocatesInferredContainers(fn.Body) {
			wrapFuncBodyInLazyAutoRegion(&fn)
		}
		if !ft.RegionPolymorphic && !funcHasArenaParam(&fn) && len(fn.Body) != 0 && returnTypeIsStaticRef(fn.ReturnType) {
			why = "static-ref return"
			return
		}
		a.wrapRegionPolyCallerBodies([]*ast.FuncDecl{&fn})
		if len(cs.regionBacked) != 0 {
			a.regionPolyFn = &fn
			needsStoreHost := a.bodyCallsStoreNeedingOutsideRegion(fn.Body, cs.storeNeeds, cs.regionBacked)
			a.regionPolyFn = nil
			if needsStoreHost && (cs.exportTarget[fn.Name] || funcIsRegionStoreEntryPoint(&fn) || a.funcOwnsRegion(&fn)) {
				wrapFuncBodyInLazyAutoRegion(&fn)
			}
		}
	})
	if why != "" {
		return nil, why
	}
	return fn.Body, ""
}

// shadowFastPrepare compares fast preparation with the scratch's prepared body.
func (r *RetainedAnalysis) shadowFastPrepare(a *Analyzer, live, edit, prepared *ast.FuncDecl, sd scopedDecl) {
	body, why := r.fastPrepareBody(a, live, edit, sd)
	if why != "" {
		fmt.Fprintf(os.Stderr, "[fast-prepare] %s: declined (%s)\n", edit.Name, why)
		return
	}
	got := *prepared
	got.Body = body
	if unparse.FormatDecl(&got) == unparse.FormatDecl(prepared) {
		fmt.Fprintf(os.Stderr, "[fast-prepare] %s: same\n", edit.Name)
		return
	}
	fmt.Fprintf(os.Stderr, "[fast-prepare] %s: DIFFERS\n--- scratch\n%s\n--- fast\n%s\n", edit.Name, unparse.FormatDecl(prepared), unparse.FormatDecl(&got))
}

// retainedFactTables are the program-wide lookups the declaration phase's per-function
// steps consult, built once from the live declarations.
type retainedFactTables struct {
	funcByName map[string]*ast.FuncDecl
	funcScopes map[*ast.FuncDecl]scopedDecl
	permRoots  map[string]bool
	// funcTypeIDs names each declared function's type by its declaration (position and name),
	// which stays the same across processes, unlike the type's address.
	funcTypeIDs map[*FuncType]string
}

func (r *RetainedAnalysis) factTables() *retainedFactTables {
	if r.tables != nil {
		return r.tables
	}
	t := &retainedFactTables{funcByName: map[string]*ast.FuncDecl{}, funcScopes: map[*ast.FuncDecl]scopedDecl{}, permRoots: collectProgramLifetimeRoots(r.decls), funcTypeIDs: map[*FuncType]string{}}
	ambiguous := map[*FuncType]bool{}
	for fn, sym := range r.a.funcDeclSymbols {
		ft, ok := sym.Type.(*FuncType)
		if !ok || ft == nil {
			continue
		}
		id := retainedLineKey(fn.Pos()) + "|" + fn.Name
		if prev, seen := t.funcTypeIDs[ft]; seen && prev != id {
			ambiguous[ft] = true
		}
		t.funcTypeIDs[ft] = id
	}
	for ft := range ambiguous {
		delete(t.funcTypeIDs, ft)
	}
	cands, namespaceOf := collectRegionPolyCandidateFuncsScoped(r.decls)
	for _, fn := range cands {
		if fn == nil {
			continue
		}
		t.funcByName[fn.Name] = fn
		if q := joinQualifiedName(namespaceOf[fn], fn.Name); q != fn.Name {
			t.funcByName[q] = fn
		}
	}
	for _, sd := range r.decls {
		switch n := sd.Decl.(type) {
		case *ast.FuncDecl:
			t.funcScopes[n] = sd
		case *ast.ImplDecl:
			for _, m := range n.Members {
				if fn, ok := m.(*ast.FuncDecl); ok {
					t.funcScopes[fn] = sd
				}
			}
		}
	}
	r.tables = t
	return t
}

// localFactDigest names every result a declaration-phase step derives from fn's body alone,
// evaluated against the live program-wide state, with fn standing in for live. Two parses of
// one function with equal digests give the declaration phase equal inputs.
func (r *RetainedAnalysis) localFactDigest(a *Analyzer, live, fn *ast.FuncDecl, sd scopedDecl) string {
	sym := a.funcDeclSymbols[live]
	var ft *FuncType
	if sym != nil {
		ft, _ = sym.Type.(*FuncType)
	}
	if ft == nil || r.classify == nil {
		return ""
	}
	tables := r.factTables()
	a.funcDeclSymbols[fn] = sym
	defer delete(a.funcDeclSymbols, fn)
	unknownCallee := false
	var b strings.Builder
	a.withResolutionContext(sd.Namespace, sd.Usings, func() {
		a.regionPolyFn = fn
		fmt.Fprintf(&b, "poly=%v,%v,%v,%v;", a.functionReturnsRegionAllocatedValue(fn), functionBuildsAndReturnsLocalContainer(fn), functionBuildsAndReturnsLocalStruct(fn, ft), a.functionWritesHandleToScalarOutParam(fn))
		fmt.Fprintf(&b, "alloc=%v;callsPoly=%v;", bodyAllocatesInferredContainers(fn.Body), a.bodyCallsRegionPolyOutsideRegion(fn.Body))
		backed := a.collectRegionBackedPackedEnums([]*ast.FuncDecl{fn})
		fmt.Fprintf(&b, "backed=%v;", slices.Sorted(maps.Keys(backed)))
		fmt.Fprintf(&b, "builds=%v;bare=%v;", slices.Sorted(maps.Keys(a.funcConstructsPackedEnumRoots(fn))), slices.Sorted(maps.Keys(a.funcBarePackedVariantRoots(fn))))
		callees := map[*FuncType]bool{}
		a.collectCalleeFuncTypes(fn.Body, callees)
		names := []string{}
		for c := range callees {
			id, known := tables.funcTypeIDs[c]
			if !known {
				unknownCallee = true
				break
			}
			names = append(names, id)
		}
		slices.Sort(names)
		fmt.Fprintf(&b, "callees=%v;", names)
		fmt.Fprintf(&b, "storeHost=%v;", a.bodyCallsStoreNeedingOutsideRegion(fn.Body, r.classify.storeNeeds, r.classify.regionBacked))
		a.regionPolyFn = nil
		fwd := collectRegionParamForwarding([]*ast.FuncDecl{fn}, tables.funcByName)[fn]
		for _, p := range fn.Params {
			fmt.Fprintf(&b, "p:%s=%v,%v,%v,%v,%v,%v,%v,%v,%v;", p.Name,
				paramContainerIsGrownNeedingRegion(fn.Body, p.Name, tables.permRoots), paramContainerReassignedFromLiteral(fn.Body, p.Name),
				paramFieldAssignedFreshContainer(fn.Body, p.Name, tables.funcByName), paramFieldContainerIsGrown(fn.Body, p.Name),
				a.containerParamGrownWithRegionValue(fn, p.Name, tables.funcByName, tables.funcScopes), a.hasOtherFreshFieldTarget(fn, p.Name),
				paramStoresBodyLocal(fn, p.Name), paramContainerIsGrown(fn.Body, p.Name), paramReturnedBare(fn.Body, p.Name))
			fmt.Fprintf(&b, "fwd=%v,%v;", forwardTargetNames(fwd.direct[p.Name]), forwardTargetNames(fwd.fields[p.Name]))
		}
		pidx := map[string]int{}
		for i, p := range fn.Params {
			pidx[p.Name] = i
		}
		var stores []string
		a.walkParamStores(fn.Body, pidx, a.paramStoreTargets,
			func(src, tgt string) { stores = append(stores, src+">"+tgt) },
			func(src string) { stores = append(stores, src+">*") },
			func(c *ast.FuncDecl) { stores = append(stores, "@"+c.Name) })
		slices.Sort(stores)
		fmt.Fprintf(&b, "stores=%v;", stores)
	})
	if unknownCallee {
		return "" // a callee without a stable identity: the digest cannot vouch for it
	}
	if os.Getenv("ELISACORE_RETAINED_FAST_DEBUG") == "1" {
		return b.String()
	}
	// Stored per baseline function in the saved state: a fixed-size hash keeps it small.
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// forwardTargetNames names each target by its callee's declaration, which stays the same
// across processes, unlike the callee's address.
func forwardTargetNames(ts []regionForwardTarget) string {
	names := make([]string, 0, len(ts))
	for _, t := range ts {
		callee := "<nil>"
		if t.callee != nil {
			callee = retainedLineKey(t.callee.Pos()) + "|" + t.callee.Name
		}
		names = append(names, fmt.Sprintf("%s@%d", callee, t.argPos))
	}
	return "[" + strings.Join(names, " ") + "]"
}

// shadowPredict reports, beside the scratch comparison's verdict, whether every edited
// function's local facts are unchanged (the condition fast preparation will rely on).
func (r *RetainedAnalysis) shadowPredict(a *Analyzer, edits [][3]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl, diff string) {
	same := true
	for _, e := range edits {
		live, old, edit := e[0], e[1], e[2]
		before, ok := r.digestOf[old]
		if !ok {
			before = r.localFactDigest(a, live, old, scoped[live])
		}
		after := r.localFactDigest(a, live, edit, scoped[live])
		if r.digestOf == nil {
			r.digestOf = map[*ast.FuncDecl]string{}
		}
		r.digestOf[edit] = after
		if before == "" || before != after {
			same = false
			fmt.Fprintf(os.Stderr, "[fast-predict] %s facts differ\n--- before\n%s\n--- after\n%s\n", edit.Name, before, after)
		}
	}
	verdict := "UNSAFE"
	switch {
	case same && diff == "":
		verdict = "agree-skip"
	case !same && diff != "":
		verdict = "agree-scratch"
	case !same && diff == "":
		verdict = "conservative"
	}
	fmt.Fprintf(os.Stderr, "[fast-predict] %d edits facts-same=%v scratch-diff=%q -> %s\n", len(edits), same, diff, verdict)
}

// fastPathBodies returns the edited bodies prepared without the scratch, or a reason the
// update must wait for it. Only line-preserving edits to function bodies qualify; each
// edited function's declaration-level facts must equal its previous version's, and no
// declaration-phase diagnostic may lie inside it.
func (r *RetainedAnalysis) fastPathBodies(a *Analyzer, edits [][3]*ast.FuncDecl, scoped map[*ast.FuncDecl]scopedDecl, oldDecls []ast.Decl) ([][]ast.Stmt, string) {
	if r.pre == nil || r.classify == nil {
		return nil, "no retained baseline"
	}
	bodies := make([][]ast.Stmt, len(edits))
	for i, e := range edits {
		live, old, edit := e[0], e[1], e[2]
		op := old.Pos()
		end := int(^uint(0) >> 1)
		for _, d := range oldDecls {
			if p := d.Pos(); p.File == op.File && p.Line > op.Line && p.Line < end {
				end = p.Line
			}
		}
		for _, d := range r.pre.diags {
			if d.Pos.File == op.File && d.Pos.Line >= op.Line && d.Pos.Line < end {
				return nil, edit.Name + " holds a declaration-phase diagnostic"
			}
		}
		if live.IsLaw {
			return nil, edit.Name + " is a law"
		}
		if classifierFoldShape(old) || classifierFoldShape(edit) {
			return nil, edit.Name + " may fold into a classifier table"
		}
		if r.appendOnlyReports(a, edit, scoped[live]) {
			return nil, edit.Name + " breaks an append-only store rule"
		}
		before, ok := r.digestOf[old]
		if !ok {
			before = r.localFactDigest(a, live, old, scoped[live])
		}
		after := r.localFactDigest(a, live, edit, scoped[live])
		if before == "" || before != after {
			if os.Getenv("ELISACORE_ANALYSIS_CACHE_DEBUG") == "1" || os.Getenv("ELISACORE_RETAINED_FAST_DEBUG") == "1" {
				fmt.Fprintf(os.Stderr, "[fast-path] %s precomputed=%v\n--- before\n%s\n--- after\n%s\n", edit.Name, ok, before, after)
			}
			return nil, edit.Name + " changes declaration-level facts"
		}
		body, why := r.fastPrepareBody(a, live, edit, scoped[live])
		if why != "" {
			return nil, edit.Name + ": " + why
		}
		if r.digestOf == nil {
			r.digestOf = map[*ast.FuncDecl]string{}
		}
		r.digestOf[edit] = after
		bodies[i] = body
	}
	return bodies, ""
}

// precomputeBaselineDigests records each baseline function's fact digest while its body is
// still in memory. A saved state omits the bodies of its comparison parse, so a restored
// update could not compute an edited function's "before" digest itself.
func (r *RetainedAnalysis) precomputeBaselineDigests() {
	if retainedFastPrepareMode != "on" || r.a == nil || r.pristine == nil || r.classify == nil {
		return
	}
	type fnKey struct {
		line string
		name string
	}
	liveFns := map[fnKey][]*ast.FuncDecl{}
	for _, d := range retainedWalkDecls(r.a.file) {
		if f, ok := d.(*ast.FuncDecl); ok {
			k := fnKey{retainedLineKey(f.Pos()), f.Name}
			liveFns[k] = append(liveFns[k], f)
		}
	}
	scoped := make(map[*ast.FuncDecl]scopedDecl, len(r.decls))
	for _, sd := range r.decls {
		if f, ok := sd.Decl.(*ast.FuncDecl); ok {
			scoped[f] = sd
		}
	}
	if r.digestOf == nil {
		r.digestOf = map[*ast.FuncDecl]string{}
	}
	for _, d := range retainedWalkDecls(r.pristine) {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || len(fn.Body) == 0 {
			continue
		}
		if _, done := r.digestOf[fn]; done {
			continue
		}
		cands := liveFns[fnKey{retainedLineKey(fn.Pos()), fn.Name}]
		if len(cands) != 1 {
			continue
		}
		sd, known := scoped[cands[0]]
		if !known {
			continue
		}
		r.digestOf[fn] = r.localFactDigest(r.a, cands[0], fn, sd)
	}
}
