package semantic

import (
	"crypto/sha256"
	"elisacore/src/ast"
	"elisacore/src/unparse"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// RetainedFastPrepareSetting resolves only configuration. Explicit selections
// (including empty/off/shadow) are preserved. Certificates and independent
// scratch fallback remain the sole authority for each actual update.
func RetainedFastPrepareSetting() string {
	if value, explicit := os.LookupEnv("ELISACORE_RETAINED_FAST_PREPARE"); explicit {
		return value
	}
	return "on"
}

var retainedFastPrepareMode = RetainedFastPrepareSetting()

// A body can change classifier preparation decisions without changing its callable type.
// Keep the complete per-function decision independently of mutable syntax.
type retainedClassifyPreparation struct {
	RegionPolymorphic, ReturnsCallerArena     bool
	OwnsRegion, Entry, Export, NeedsStoreHost bool
	Wrapper                                   string
}

// retainedClassifyState contains the declaration-phase classifier inputs and
// decisions needed to validate retained body preparation.
type retainedClassifyState struct {
	preparation  map[*ast.FuncDecl]retainedClassifyPreparation
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

// A successful scratch-backed declaration update does not recapture/remap these
// snapshots onto the new live declaration graph. Drop them together rather than
// mixing new lookup tables with old classification/store/append-only facts.
// Ordinary retained updates continue through scratch; a fresh AnalyzeRetained
// invocation supplies the next independently qualified fast baseline.
func (r *RetainedAnalysis) invalidateFastPrepareBaseline(reason string) {
	r.classify, r.appendOnly, r.tables, r.digestOf = nil, nil, nil, nil
	r.fastPrepareInvalidated = reason
}

// appendOnlyReports reports whether the append-only store check flags edit. It records
// nothing: a flagged edit goes through the scratch, which reports it.
func (r *RetainedAnalysis) appendOnlyReports(a *Analyzer, edit *ast.FuncDecl, sd scopedDecl) bool {
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
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
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
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
	proofProbe := a.beginProofReportProbe()
	defer proofProbe.restore()
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
		// Owning any region selects signature-only packed-store threading,
		// instead of the transitive callee store set. A change is an ABI change
		// even if allocations, callees and outside-region calls are unchanged.
		fmt.Fprintf(&b, "ownsRegion=%v;", a.funcOwnsRegion(fn))
		fmt.Fprintf(&b, "poly=%v,%v,%v,%v;", a.functionReturnsRegionAllocatedValue(fn), functionBuildsAndReturnsLocalContainer(fn), functionBuildsAndReturnsLocalStruct(fn, ft), a.functionWritesHandleToScalarOutParam(fn))
		fmt.Fprintf(&b, "alloc=%v;callsPoly=%v;", bodyAllocatesInferredContainers(fn.Body), a.bodyCallsRegionPolyOutsideRegion(fn.Body))
		backed := a.collectRegionBackedPackedEnums([]*ast.FuncDecl{fn})
		fmt.Fprintf(&b, "backed=%v;", slices.Sorted(maps.Keys(backed)))
		callees := map[*FuncType]bool{}
		constructors, bareVariants := a.scanPackedStoreBody(fn, callees, nil)
		fmt.Fprintf(&b, "builds=%v;bare=%v;", slices.Sorted(maps.Keys(constructors)), slices.Sorted(maps.Keys(bareVariants)))
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
		var stores []string
		// The digest records all resolved calls, including those that cannot
		// relay a store from one of this function's parameters.
		a.walkParamStores(fn.Body, nil, a.paramStoreTargets,
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
