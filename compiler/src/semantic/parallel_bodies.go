package semantic

import (
	"fmt"
	"maps"
	"math"
	"os"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"elisacore/src/ast"
)

// parallelBodyWorkers is the number of workers that analyze function bodies
// concurrently: ELISA_PARALLEL_BODIES when set (<= 1 keeps the serial path),
// otherwise one per CPU up to eight, past which the seed stops scaling.
func parallelBodyWorkers() int {
	if v, set := os.LookupEnv("ELISA_PARALLEL_BODIES"); set {
		n, _ := strconv.Atoi(v)
		return n
	}
	// Body analysis is allocation-bound: workers on efficiency cores slow the
	// performance-core workers down more than they add (seed, 4P+6E: 4-5 workers
	// 30-31 s, 8 workers 32-35 s).
	if p := performanceCores(); p > 0 {
		return min(p+1, runtime.NumCPU())
	}
	return min(runtime.NumCPU(), 8)
}

// analyzeDeclsParallel analyzes non-function declarations serially, then splits
// the function-bearing declarations into contiguous chunks in source order. Each
// chunk runs on its own copy of the Analyzer; the copies' state is merged back in
// chunk order, so diagnostics keep source order.
func (a *Analyzer) analyzeDeclsParallel(decls []scopedDecl, workers int) {
	if a.optimizationSymbolIdentityOverlay != nil {
		a.rejectOptimizationSymbolIdentityOverlayOperation("parallel declaration scheduling")
		return
	}
	if a.storeScanBinderOverlay != nil {
		a.rejectStoreScanBinderOverlayOperation("parallel declaration scheduling")
		return
	}
	if a.storeScanParamFactsOverlay != nil {
		a.rejectStoreScanParamFactsOverlayOperation("parallel declaration scheduling")
		return
	}
	if a.noStorageOverlay != nil {
		a.rejectNoStorageOverlayOperation("parallel declaration scheduling")
		return
	}
	if a.iterBinderElementOverlay != nil {
		a.rejectIterBinderElementOverlayOperation("parallel declaration scheduling")
		return
	}
	if a.exprFactsOverlay != nil {
		a.rejectExprFactsOverlayOperation("parallel declaration scheduling")
		return
	}
	if a.exprTypeOverlay != nil {
		a.rejectExprTypeOverlayOperation("parallel declaration scheduling")
		return
	}
	var bodies []scopedDecl
	declOrder := make(map[*ast.FuncDecl]int)
	nextDeclOrder := 0
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			declOrder[n] = nextDeclOrder
			nextDeclOrder++
			// Laws are read through refinement types and aliases the call graph does
			// not see; they are small and pure, so they are settled before the fork.
			if n.IsLaw {
				a.analyzeScopedDecl(scoped)
				continue
			}
			bodies = append(bodies, scoped)
		case *ast.ImplDecl:
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					declOrder[fn] = nextDeclOrder
					nextDeclOrder++
				}
			}
			bodies = append(bodies, scoped)
		default:
			a.analyzeScopedDecl(scoped)
		}
	}
	if len(bodies) < workers {
		workers = len(bodies)
	}

	reportCrossDeclSharing(bodies)
	t0 := time.Now()
	reportBodyGraph(bodies)
	sched := planBodySchedule(bodies, workers)
	if parallelReport {
		fmt.Fprintf(os.Stderr, "[parallel-bodies] recursive summary seeds %d of %d bodies\n", len(sched.cyclic), len(bodies))
	}
	// Callee-first, as the serial pass reaches each callee from its callers.
	var ordered []scopedDecl
	for _, unit := range sched.units {
		for _, b := range unit {
			ordered = append(ordered, bodies[b])
		}
	}
	tsnap := time.Now()
	at := map[*ast.FuncDecl][2]int{}
	slots := make([]atomic.Int32, len(bodies))
	snapshots := make([]*ast.SyntaxSnapshot, len(bodies))
	frozen := make([]ast.Decl, len(bodies)) // pristine copies where the snapshot declines
	declined := 0
	for i, scoped := range bodies {
		slots[i].Store(-1)
		// One snapshot per declaration: the capture budget is per graph, and a
		// worker only ever needs a single declaration back.
		if snap, err := ast.NewSyntaxSnapshot(&ast.File{Decls: []ast.Decl{scoped.Decl}}, 1<<40); err == nil {
			snapshots[i] = snap
		} else {
			frozen[i] = copyASTDecl(scoped.Decl)
			declined++
			if os.Getenv("ELISA_PARALLEL_DECLINE_LOG") != "" {
				fmt.Fprintf(os.Stderr, "[parallel-decline] %T: %v\n", scoped.Decl, err)
				if declined <= 3 {
					fmt.Fprintf(os.Stderr, "[parallel-decline-storage] %v: %s\n", scoped.Decl.Pos(), firstASTStorageOverlap(scoped.Decl))
				}
			}
		}
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			at[n] = [2]int{i, -1}
		case *ast.ImplDecl:
			for m, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok {
					at[fn] = [2]int{i, m}
				}
			}
		}
	}
	if parallelReport {
		fmt.Fprintf(os.Stderr, "[parallel-bodies] snapshots %v (%d declined)\n", time.Since(tsnap).Round(time.Millisecond), declined)
	}
	// Freeze worker syntax before summary inference resolves patterns. Its
	// ResolvedArgs point into Args elements, which are intentional interior aliases
	// that the generated syntax snapshot conservatively declines. Workers rebuild
	// those derived resolutions while analyzing their private pristine bodies.
	a.precomputeSharedCalleeFacts(ordered, sched.cyclic)
	// Per-return element states are scratch for the body analysis that owns them; a
	// precompute pass that met a return must not leave a merged-into prior behind.
	a.returnElementStmtStates, a.returnElementStmtUnknown = nil, nil
	tp := time.Now()
	base := cloneAnalyzerValueForParallel(a)
	clones := make([]*Analyzer, workers)
	smtFlights := &sync.Map{}
	for i := range clones {
		w := new(Analyzer)
		settable(reflect.ValueOf(w).Elem()).Set(cloneAnalyzerValueForBodyWorker(a))
		isolateWorkerScopes(a, w)
		w.resolvedTypeNames = make(map[ast.TypeExpr]string)
		w.parallel = &parallelWorkerState{
			worker: i, snapshots: snapshots, frozen: frozen, at: at, slots: slots, smtFlights: smtFlights,
			funcTypeMu: &sched.mu,
			shadows:    map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{},
			predicateMemoBases: parallelPredicateMemoBases{
				affineHandles: a.affineHandleMemo, borrowedOwnerRefs: a.borrowedOwnerRefMemo,
				regionRefs: a.regionRefsMemo, mutableBorrowCapabilities: a.mutableBorrowCapabilityMemo,
				storeScanAddresses: a.storeScanAddressMemo, storeScanPlainValues: a.storeScanPlainValueMemo,
				storeScanProgramLived: a.storeScanProgramLivedMemo,
			},
			programLivedTypesBase: a.programLivedTypes, smtLocalDefDepsBase: a.smtLocalDefDeps,
			resolvedTypeNamesBase: a.resolvedTypeNames, abstractParamRegionRefMemoBase: a.abstractParamRegionRefMemo,
			abstractRefRestartMemoBase: a.abstractRefRestartMemo,
		}
		if a.retained != nil {
			// What a retained analysis records during bodies: per-function diagnostic
			// windows and return-borrow summary dependencies.
			w.retained = &RetainedAnalysis{windows: map[*ast.FuncDecl][2]int{}, rbDeps: map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}}
		}
		clones[i] = w
	}
	baseDiagnostics := len(a.diagnostics)
	// Small blocks dealt round-robin balance the expensive stretches of a file
	// across workers while keeping the assignment independent of timing.
	type span struct{ worker, from, to int }
	spans := make([]span, len(bodies))
	t1 := time.Now()
	var wg sync.WaitGroup
	for i := range clones {
		wg.Add(1)
		go func(w *Analyzer, i int) {
			defer wg.Done()
			defer w.closeSMT()
			ts := time.Now()
			n := 0
			var waited, blocked time.Duration
			ranHere := map[int]bool{}
			for {
				u := sched.claim(i)
				if u < 0 {
					break
				}
				for _, d := range sched.units[u] {
					slots[d].Store(int32(i))
				}
				tw := time.Now()
				// Import every transitive dependency, not only direct ones: a callee's facts
				// can rest on its own callees' (a spec's equation on a helper's verified
				// termination), and which of those a worker happened to import for an
				// earlier unit depends on timing. Dependencies of finished units are
				// finished, so this never waits longer than the direct imports.
				for _, du := range sched.depClosure(u, func(d int) bool { return ranHere[d] || w.parallel.importedUnits[d] }) {
					if sched.worker[du] != i && !w.parallel.importedUnit(du) {
						blocked += sched.importUnit(w, du)
					}
				}
				waited += time.Since(tw)
				tu := time.Now()
				for _, d := range sched.units[u] {
					from := len(w.diagnostics)
					w.analyzeScopedDecl(bodies[d])
					spans[d] = span{worker: i, from: from, to: len(w.diagnostics)}
					n++
				}
				sched.took[u] = time.Since(tu)
				ranHere[u] = true
				if el := time.Since(tu); el > 300*time.Millisecond && os.Getenv("ELISA_PARALLEL_GRAPH_REPORT") != "" {
					first := bodies[sched.units[u][0]].Decl
					label := fmt.Sprintf("%T", first)
					if fn, ok := first.(*ast.FuncDecl); ok {
						label = fn.Name
					}
					fmt.Fprintf(os.Stderr, "[parallel-unit] worker %d unit %d: %d bodies %v (%s @ %v)\n", i, u, len(sched.units[u]), el.Round(time.Millisecond), label, first.Pos())
				}
				sched.exportUnit(w, u)
				sched.finish(u)
			}
			if parallelReport {
				fmt.Fprintf(os.Stderr, "[parallel-bodies] worker %d: %d decls %v (blocked %v, importing %v)\n", i, n, time.Since(ts).Round(time.Millisecond), blocked.Round(time.Millisecond), (waited - blocked).Round(time.Millisecond))
			}
		}(clones[i], i)
	}
	wg.Wait()
	t2 := time.Now()
	sched.reportMeasured()
	workerDiags := make([][]Diagnostic, workers)
	workerRecords := make([]*RetainedAnalysis, workers)
	for i, w := range clones {
		workerDiags[i] = w.diagnostics
		workerRecords[i], w.retained = w.retained, a.retained
		w.diagnostics = a.diagnostics[:baseDiagnostics:baseDiagnostics]
		w.reportedDiagnostics = a.reportedDiagnostics
		w.globalScope, w.currentScope, w.smtTextFree = a.globalScope, a.currentScope, a.smtTextFree
		p := w.parallel
		w.parallel = nil
		a.mergeWorkerProofContributions(w, p)
		mergeRequiresReport(a, w, base)
		mergeValue(settable(reflect.ValueOf(a).Elem()), reflect.ValueOf(w).Elem(), base, p)
		mergeParallelSMTStats(&a.smtStats, w.smtStats)
		p.publishFuncTypes()
	}
	// Workers merge their appended work lists in worker order; the late
	// return-borrow checks report in the order serial analysis queues them.
	order := func(fn *ast.FuncDecl) int {
		if loc, ok := at[fn]; ok {
			return loc[0]<<16 | (loc[1] + 1)
		}
		return math.MaxInt
	}
	annotationOrder := func(fn *ast.FuncDecl) int {
		if fn != nil {
			if idx, ok := declOrder[fn]; ok {
				return idx
			}
		}
		return math.MaxInt
	}
	sort.SliceStable(a.annotatedFuncs, func(i, j int) bool {
		return annotationOrder(a.annotatedFuncs[i].Decl) < annotationOrder(a.annotatedFuncs[j].Decl)
	})
	sort.SliceStable(a.returnBorrowLateFrames, func(i, j int) bool {
		return order(a.returnBorrowLateFrames[i].frame.fn) < order(a.returnBorrowLateFrames[j].frame.fn)
	})
	// The merge folds a worker's current candidate table into the root's, whatever
	// resolution context each was built for; drop the cache rather than mix contexts.
	a.visibleCandidates = visibleCandidateCache{}
	a.diagnostics = a.diagnostics[:baseDiagnostics]
	// merged[w][k]: how many diagnostics precede worker w's k-th in the merged list, for
	// rebasing a retained analysis's windows (appendDiagnostic can drop a duplicate).
	var merged [][]int
	if a.retained != nil {
		merged = make([][]int, workers)
		for i := range merged {
			merged[i] = make([]int, len(workerDiags[i])+1)
		}
	}
	for _, s := range spans {
		for k, d := range workerDiags[s.worker][s.from:s.to] {
			if merged != nil {
				merged[s.worker][s.from+k] = len(a.diagnostics)
			}
			a.appendDiagnostic(d)
		}
		if merged != nil {
			merged[s.worker][s.to] = len(a.diagnostics)
		}
	}
	if r := a.retained; r != nil {
		if r.windows == nil {
			r.windows = map[*ast.FuncDecl][2]int{}
		}
		if r.rbDeps == nil {
			r.rbDeps = map[*ast.FuncDecl]map[*ast.FuncDecl]bool{}
		}
		for i, rec := range workerRecords {
			for fn, win := range rec.windows {
				// Only a window inside its own declaration's span was kept in the merge.
				if loc, ok := at[fn]; ok {
					if s := spans[loc[0]]; s.worker == i && s.from <= win[0] && win[1] <= s.to {
						r.windows[fn] = [2]int{merged[i][win[0]], merged[i][win[1]]}
					}
				}
			}
			for owner, deps := range rec.rbDeps {
				if r.rbDeps[owner] == nil {
					r.rbDeps[owner] = map[*ast.FuncDecl]bool{}
				}
				for fn := range deps {
					r.rbDeps[owner][fn] = true
				}
			}
		}
	}
	if parallelReport {
		fmt.Fprintf(os.Stderr, "[parallel-bodies] precompute %v, clone %v, bodies %v, merge %v\n", tp.Sub(t0).Round(time.Millisecond), t1.Sub(tp).Round(time.Millisecond), t2.Sub(t1).Round(time.Millisecond), time.Since(t2).Round(time.Millisecond))
	}
}

func cloneAnalyzerValueForParallel(a *Analyzer) reflect.Value {
	return cloneAnalyzerValueForParallelMode(a, false)
}

func cloneAnalyzerValueForBodyWorker(a *Analyzer) reflect.Value {
	return cloneAnalyzerValueForParallelMode(a, true)
}

func cloneAnalyzerValueForParallelMode(a *Analyzer, useMemoOverlays bool) reflect.Value {
	return cloneAnalyzerValueForParallelPolicy(a, useMemoOverlays, false)
}

// A specialization imports exactly the current/private declarations through
// specializationContext.copyDecl. Its analyzer never queries unrelated function
// analyses; public Result queries retain canonical fallback after publication.
func cloneAnalyzerValueForSpecialization(a *Analyzer) reflect.Value {
	if a != nil && (a.exprTypeOverlay != nil || a.exprFactsOverlay != nil || a.iterBinderElementOverlay != nil || a.noStorageOverlay != nil || a.storeScanParamFactsOverlay != nil || a.storeScanBinderOverlay != nil || a.optimizationSymbolIdentityOverlay != nil) {
		panic("specialization construction requires a canonical analyzer, not another construction overlay")
	}
	return cloneAnalyzerValueForParallelPolicy(a, true, true)
}

var parallelAnalyzerFieldNames = func() []string {
	t := reflect.TypeFor[Analyzer]()
	names := make([]string, t.NumField())
	for i := range names {
		names[i] = t.Field(i).Name
	}
	return names
}()

func cloneAnalyzerValueForParallelPolicy(a *Analyzer, useMemoOverlays, ownedFunctionAnalyses bool) reflect.Value {
	if a == nil {
		return reflect.Value{}
	}
	source := reflect.ValueOf(a).Elem()
	cloned := reflect.New(source.Type()).Elem()
	cloned.Set(source)
	shareDeclarationMaps := true
	// Preserve deepCloneValue's value policy if OptimizationFacts ever grows
	// inline maps/slices: that schema keeps the ordinary reflective copy.
	useExprFactsOverlay := ownedFunctionAnalyses && !typeHoldsMapInline(reflect.TypeFor[OptimizationFacts]())
	for i := 0; i < source.NumField(); i++ {
		field := source.Field(i)
		name := parallelAnalyzerFieldNames[i]
		if ownedFunctionAnalyses && name == "proofLifecycle" {
			// The private construction journal contains only concrete work;
			// publication retains the canonical Result's full public report.
			settable(cloned.Field(i)).Set(reflect.ValueOf(a.specializationProofReportLifecycle()))
			continue
		}
		if name == "proofLifecycle" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(a.forkProofReportLifecycle()))
			continue
		}
		if ownedFunctionAnalyses && name == "proofReport" {
			// Concrete producers append only construction-local observations.
			// Published specialization views inherit the complete canonical
			// Result report and summary, then discard this analyzer. Borrowing
			// its event prefix would copy the whole program on the first append.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if ownedFunctionAnalyses && name == "visibleCandidates" {
			// This memo contains only candidate names derived from lexical
			// context and aliases. Rebuild the few private lookups on demand
			// instead of copying every context/name table in the program.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if ownedFunctionAnalyses && name == "headerFunctionNames" {
			// The published counts map is immutable. A worker that needs a new
			// index replaces its own whole value, so sharing the current map avoids
			// copying every function name for each specialization.
			continue
		}
		if ownedFunctionAnalyses && (name == "diagnostics" || name == "reportedDiagnostics") {
			// Reanalysis must re-emit its own errors, even when position/text
			// matches an inherited canonical diagnostic. Skip before cloning
			// either the inherited slice or its potentially large dedup map.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if name == "optimizationSymbolIdentityOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerOptimizationSymbolIdentityOverlay(a.optimizationSymbolIdentityOverlay)))
			continue
		}
		if ownedFunctionAnalyses && name == "optimizationSymbolIDs" {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "storeScanBinderOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerStoreScanBinderOverlay(a.storeScanBinderOverlay)))
			continue
		}
		if name == "storeScanParamFactsOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerStoreScanParamFactsOverlay(a.storeScanParamFactsOverlay)))
			continue
		}
		if name == "noStorageOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerNoStorageOverlay(a.noStorageOverlay)))
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanBinderTypes" {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanNoStorageCache" {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanSharesCache" {
			// This memo contains immutable type-property results. Inherit settled
			// rows through noStorageOverlay and keep new rows private to this
			// specialization instead of copying the whole program cache.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "compilerBuiltinHelperCalls" {
			// copyDecl rekeys only the owned call facts needed by this
			// specialization. Keep the whole-program canonical table as a
			// read-only overlay base instead of cloning every helper call.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "returnElementStmtUnknown" {
			// copyDecl rekeys only owned return rows. Inherit the remaining
			// whole-program facts through the overlay base instead of copying all.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "indexBoundsRuntimeSites" {
			// Runtime guard sites are observational telemetry. Specialization
			// re-analyzes its private syntax and publishes the canonical summary,
			// so start with a private empty ledger instead of copying every site.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanParamWriteCache" {
			// This reachability memo is shared as an immutable base. Keep only
			// private rows produced while analyzing the concrete specialization.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "iterBinderElementOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerIterBinderElementOverlay(a.iterBinderElementOverlay)))
			continue
		}
		if ownedFunctionAnalyses && (name == "iterBinderElementStates" || name == "iterBindingSources") {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "exprFactsOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerExprFactsOverlay(a.exprFactsOverlay)))
			continue
		}
		if useExprFactsOverlay && name == "exprFacts" {
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if name == "exprTypeOverlay" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneAnalyzerExprTypeOverlay(a.exprTypeOverlay)))
			continue
		}
		if ownedFunctionAnalyses && name == "exprTypes" {
			// Private observations only: canonical reads use the immutable base.
			// Skip its entire map before reflective cloning or typed cloning.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanParamFactsCache" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreScanParamFactsConstructionMap(a.storeScanParamFactsCache, specializationParamFactsCopySchemaValid)))
			continue
		}
		if ownedFunctionAnalyses && name == "functionAnalyses" {
			// Skip BEFORE deepCloneValue: cloning then clearing still performs
			// a whole-program map traversal in the serialized specialization path.
			settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			continue
		}
		if ownedFunctionAnalyses && name == "regionStacks" {
			// copyDecl imports and isolates assignments for owned region syntax
			// from the canonical source. Private analysis only writes fresh rows;
			// published queries inherit foreign assignments through the root view.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && (name == "storeScanCallWriteCache" || name == "storageViewCalleeWriteCache") {
			// The call-write cache is AST-keyed, so copyDecl imports only rows for
			// syntax owned by the private declaration. Storage-view summaries use
			// composite declaration keys; specialization reads go through private
			// shadows and recompute those rows on demand. Keep this map private so
			// fresh memo entries cannot mutate the canonical analyzer.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "returnElementStmtStates" {
			// copyDecl imports and isolates statement facts for owned ReturnStmt
			// syntax. Unrelated functions' rows are never queried during private
			// specialization analysis, so avoid cloning the whole-program map.
			if field.IsNil() {
				settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			} else {
				settable(cloned.Field(i)).Set(reflect.MakeMap(field.Type()))
			}
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanBoundCache" {
			// Settled name sets are read-only; only publish new rows privately.
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.storeScanBoundCache)))
			continue
		}
		if ownedFunctionAnalyses && name == "funcDeclUsings" {
			// Usings are installed as fresh slices and only read (or copied into a
			// resolution context). Keep the index private for declaration edits,
			// while sharing its immutable per-declaration rows.
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.funcDeclUsings)))
			continue
		}
		if name == "retained" {
			// The retained analysis owns the whole analyzer; a worker records into a
			// shell of its own (analyzeDeclsParallel), merged back after the join.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && (name == "smtSolver" || name == "smtStats") {
			// A parent's live solver serializes all checks behind its mutex.
			// Workers own lazy solver sessions and account only their own work.
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && name == "storeScanTaintCache" {
			// Published taint sets are read-only. Only the cache's outer map
			// receives new entries, so give each worker an independent map
			// while preserving the settled sets (and their internal sharing).
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.storeScanTaintCache)))
			continue
		}
		if useMemoOverlays && name == "storeFlowSummaries" {
			if ownedFunctionAnalyses {
				settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreFlowSummariesForSpecialization(a.storeFlowSummaries)))
			} else {
				settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreFlowSummariesForBodyWorker(a.storeFlowSummaries)))
			}
			continue
		}
		// These caches contain lazily populated maps/slices behind pointers.
		// Rebuild them privately rather than sharing mutable pointer values.
		if useMemoOverlays && parallelBodyWorkerRebuildMap(name) {
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && parallelBodyMemoMap(name) {
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if shareDeclarationMaps && field.Kind() == reflect.Map && !field.IsNil() && parallelDeclarationMapIsReadOnly(name) {
			continue
		}
		if ownedFunctionAnalyses && cloneSpecializationStoreScanBoolSetCache(cloned.Addr().Interface().(*Analyzer), a, name) {
			continue
		}
		if ownedFunctionAnalyses && cloneSpecializationNestedSetField(cloned.Addr().Interface().(*Analyzer), a, name) {
			continue
		}
		if ownedFunctionAnalyses && name == "returnElementSummaries" {
			cloned.Addr().Interface().(*Analyzer).returnElementSummaries = cloneSpecializationReturnElementSummaries(a.returnElementSummaries)
			continue
		}
		if ownedFunctionAnalyses && name == "storeFlowSCC" {
			cloned.Addr().Interface().(*Analyzer).storeFlowSCC = cloneSpecializationFuncDeclSliceMap(a.storeFlowSCC, specializationGraphCacheSchema("storeFlowSCC"))
			continue
		}
		if ownedFunctionAnalyses && name == "callEdgeCache" {
			cloned.Addr().Interface().(*Analyzer).callEdgeCache = cloneSpecializationFuncDeclSliceMap(a.callEdgeCache, specializationGraphCacheSchema("callEdgeCache"))
			continue
		}
		if ownedFunctionAnalyses && name == "disjointCallSitesByCaller" {
			cloned.Addr().Interface().(*Analyzer).disjointCallSitesByCaller = cloneSpecializationDisjointCallSites(a.disjointCallSitesByCaller)
			continue
		}
		if ownedFunctionAnalyses && name == "returnBorrowFuncSummaries" {
			cloned.Addr().Interface().(*Analyzer).returnBorrowFuncSummaries = cloneSpecializationReturnBorrowSummaries(a.returnBorrowFuncSummaries)
			continue
		}
		if ownedFunctionAnalyses && name == "paramStoreTargets" {
			cloned.Addr().Interface().(*Analyzer).paramStoreTargets = cloneSpecializationParamStoreTargets(a.paramStoreTargets)
			continue
		}
		if ownedFunctionAnalyses && name == "storeScanFilledCache" {
			// Filled-local rows are completed fixed-point results. The scanner
			// only reads them after publication, so copy the mutable cache header
			// and share its immutable result sets.
			settable(cloned.Field(i)).Set(reflect.ValueOf(maps.Clone(a.storeScanFilledCache)))
			continue
		}
		if ownedFunctionAnalyses && cloneSpecializationOpaqueMap(cloned.Addr().Interface().(*Analyzer), a, name, field) {
			continue
		}
		settable(cloned.Field(i)).Set(deepCloneValue(settable(field)))
	}
	if ownedFunctionAnalyses {
		worker := cloned.Addr().Interface().(*Analyzer)
		worker.optimizationSymbolIdentityOverlay = &analyzerOptimizationSymbolIdentityOverlay{base: a.optimizationSymbolIDs}
		worker.storeScanBinderOverlay = &analyzerStoreScanBinderOverlay{base: a.storeScanBinderTypes}
		if specializationParamFactsCopySchemaValid {
			worker.storeScanParamFactsOverlay = &analyzerStoreScanParamFactsOverlay{base: a.storeScanParamFactsCache}
		}
		worker.noStorageOverlay = &analyzerNoStorageOverlay{
			base:          a.storeScanNoStorageCache,
			viewOnlyBase:  a.storeScanViewOnlyCache,
			sharesBase:    a.storeScanSharesCache,
			paramWrites:   a.storeScanParamWriteCache,
			helperCalls:   a.compilerBuiltinHelperCalls,
			returnUnknown: a.returnElementStmtUnknown,
		}
		worker.iterBinderElementOverlay = &analyzerIterBinderElementOverlay{base: a.iterBinderElementStates, sources: a.iterBindingSources}
		worker.exprTypeOverlay = &analyzerExprTypeOverlay{base: a.exprTypes}
		if useExprFactsOverlay {
			worker.exprFactsOverlay = &analyzerExprFactsOverlay{base: a.exprFacts}
		}
	}
	return cloned
}

// These settled caches are read-only after publication. Specializations copy
// the mutable outer maps while sharing immutable rows, or use a read-through
// base for the type-only view predicate cache.
func cloneSpecializationStoreScanBoolSetCache(dst, src *Analyzer, field string) bool {
	switch field {
	case "storeScanViewCache":
		dst.storeScanViewCache = maps.Clone(src.storeScanViewCache)
	case "storeScanScalarCache":
		dst.storeScanScalarCache = maps.Clone(src.storeScanScalarCache)
	case "storeScanReadOnlyCache":
		dst.storeScanReadOnlyCache = maps.Clone(src.storeScanReadOnlyCache)
	case "storeScanViewOnlyCache":
		if src.storeScanViewOnlyCache != nil {
			dst.storeScanViewOnlyCache = make(map[Type]bool)
		}
	default:
		return false
	}
	return true
}

func parallelBodyWorkerRebuildMap(field string) bool {
	switch field {
	case "storeScanIndexCache", "storeFlowBinderIndexes", "storeFlowInProgress",
		"storeFlowActive", "regionPointeesMemo":
		return true
	default:
		return false
	}
}

// Completed summaries never change their connectivity. Edge-kind solving does
// mutate retKind/reachKind, so give workers private summary objects and kind
// slices while retaining the settled (potentially large) connectivity matrices.
func cloneStoreFlowSummariesForBodyWorker(src map[*ast.FuncDecl]*storeFlowSummary) map[*ast.FuncDecl]*storeFlowSummary {
	if src == nil {
		return nil
	}
	out := make(map[*ast.FuncDecl]*storeFlowSummary, len(src))
	memo := make(map[*storeFlowSummary]*storeFlowSummary, len(src))
	for decl, summary := range src {
		if summary == nil {
			out[decl] = nil
			continue
		}
		if prior := memo[summary]; prior != nil {
			out[decl] = prior
			continue
		}
		cloned := *summary
		cloned.retKind = slices.Clone(summary.retKind)
		cloned.reachKind = slices.Clone(summary.reachKind)
		for i, row := range summary.reachKind {
			cloned.reachKind[i] = slices.Clone(row)
		}
		memo[summary] = &cloned
		out[decl] = &cloned
	}
	return out
}

// These caches can contain worker read-shadow identities or transient solver
// state. Keep their worker copies out of unit exports and root merges. Warm graph
// entries built before the fork contain canonical declarations and immutable
// slices, so cloning their outer maps avoids repeating whole-graph walks.
func parallelBodyWorkerLocalOnlyMap(field string) bool {
	switch field {
	case "callEdgeCache", "storeFlowSCC", "storeFlowInProgress", "storeFlowActive", "storageViewCalleeWriteCache", "regionPointeesMemo":
		return true
	// Equation eligibility rests on callees' verified termination, which the exporting
	// worker may not have known yet. Each worker recomputes it from the termination
	// facts it holds instead of importing a possibly stale verdict.
	case "definingEquationCache", "viewEquationCache", "definingEquationInProgress":
		return true
	default:
		return false
	}
}

func parallelBodyMemoMap(field string) bool {
	switch field {
	case "affineHandleMemo", "borrowedOwnerRefMemo", "regionRefsMemo", "mutableBorrowCapabilityMemo", "storeScanAddressMemo",
		"storeScanPlainValueMemo", "storeScanProgramLivedMemo",
		"programLivedTypes", "smtLocalDefDeps", "resolvedTypeNames", "abstractParamRegionRefMemo", "abstractRefRestartMemo":
		return true
	default:
		return false
	}
}

// Declaration indexes are complete before body workers start. Sharing their map
// headers avoids reflecting over large, nested indexes once per worker; mutable
// analysis caches and per-body facts still receive isolated maps.
func parallelDeclarationMapIsReadOnly(field string) bool {
	switch field {
	case "namedTypes", "staticInterfaces", "staticImpls", "effectHandlers", "permissions", "capabilityAliases",
		"extensionMethodsByName", "ufcsFunctionsByName", "ufcsByLastSegment", "functionTypes", "constValues",
		"ambientGrownCalleeParamIndex", "regionParamCalleeParamIndex", "declVisibility", "declaredFuncNames", "callDeclsByName",
		"declaredModules", "trustedRuntimeSourceFiles", "storageViewWriteDeclsByName", "aliasRefinements",
		"parametricAliasRefinements", "externLinkNames", "privateTypeNames", "privateModules", "castHooksByName",
		"initHooksByName", "dropHooks", "packedStoreBuilders", "regionPolyCandidateFnTypes",
		"regionPolyReturnsCallerArena":
		return true
	default:
		return false
	}
}

// settable makes an unexported field writable through reflection.
func settable(v reflect.Value) reflect.Value {
	if v.CanSet() || !v.CanAddr() {
		return v
	}
	return reflect.NewAt(v.Type(), unsafe.Pointer(v.UnsafeAddr())).Elem()
}

// deepCloneValue copies maps at every depth reachable through structs, arrays and
// map values, so a worker's in-place writes never reach another worker. Slices
// keep their elements but get capacity == length, so an append reallocates.
// Pointers, AST nodes and types included, stay shared.
func deepCloneValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		if v.Len() == 0 {
			return out
		}
		iter := v.MapRange()
		if v.Len() < 2 && !typeHoldsMapInline(v.Type().Elem()) {
			for iter.Next() {
				out.SetMapIndex(iter.Key(), deepCloneValue(iter.Value()))
			}
			return out
		}
		// Iterator Key/Value calls box indirect values. Reuse addressable slots;
		// SetMapIndex copies each slot's contents into the destination entry.
		var keySlot, valueSlot, structSlot reflect.Value
		if !parallelCloneDirectReflectKind(v.Type().Key().Kind()) {
			keySlot = reflect.New(v.Type().Key()).Elem()
		}
		if !parallelCloneDirectReflectKind(v.Type().Elem().Kind()) {
			valueSlot = reflect.New(v.Type().Elem()).Elem()
		}
		cloneStruct := v.Type().Elem().Kind() == reflect.Struct && typeHoldsMapInline(v.Type().Elem())
		if cloneStruct {
			structSlot = reflect.New(v.Type().Elem()).Elem()
		}
		for iter.Next() {
			var key, value reflect.Value
			if keySlot.IsValid() {
				keySlot.SetIterKey(iter)
				key = keySlot
			} else {
				key = iter.Key()
			}
			if valueSlot.IsValid() {
				valueSlot.SetIterValue(iter)
				value = valueSlot
			} else {
				value = iter.Value()
			}
			if cloneStruct {
				cloneParallelStructInto(structSlot, value)
				out.SetMapIndex(key, structSlot)
			} else {
				out.SetMapIndex(key, deepCloneValue(value))
			}
		}
		return out
	case reflect.Struct:
		if !typeHoldsMapInline(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		cloneParallelStructInto(out, v)
		return out
	case reflect.Array:
		if !typeHoldsMapInline(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.Len(); i++ {
			target := settable(out.Index(i))
			target.Set(deepCloneValue(target))
		}
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		return v.Slice3(0, v.Len(), v.Len())
	}
	return v
}

// These kinds fit directly in reflection values, so iterators do not box them.
func parallelCloneDirectReflectKind(kind reflect.Kind) bool {
	switch kind {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan, reflect.Map, reflect.Func:
		return true
	default:
		return false
	}
}

func cloneParallelStructInto(out, source reflect.Value) {
	out.Set(source)
	for i := 0; i < source.NumField(); i++ {
		target := settable(out.Field(i))
		if target.Kind() == reflect.Struct && typeHoldsMapInline(target.Type()) {
			cloneParallelStructInto(target, target)
		} else {
			target.Set(deepCloneValue(target))
		}
	}
}

var mapInlineCache sync.Map

// typeHoldsMapInline reports whether a value of t carries a map or slice without
// going through a pointer.
func typeHoldsMapInline(t reflect.Type) bool {
	if cached, ok := mapInlineCache.Load(t); ok {
		return cached.(bool)
	}
	holds := false
	switch t.Kind() {
	case reflect.Map, reflect.Slice:
		holds = true
	case reflect.Array:
		holds = typeHoldsMapInline(t.Elem())
	case reflect.Struct:
		for i := 0; i < t.NumField() && !holds; i++ {
			holds = typeHoldsMapInline(t.Field(i).Type)
		}
	}
	mapInlineCache.Store(t, holds)
	return holds
}

// mergeValue folds what a worker changed relative to base into dst: new or
// replaced map entries (nested maps merged entry by entry), slice tails appended
// past the base length, and changed scalars and pointers.
func mergeValue(dst, w, base reflect.Value, p *parallelWorkerState) {
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("optimizationSymbolIdentityOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectOptimizationSymbolIdentityOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}

	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("storeScanBinderOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectStoreScanBinderOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("storeScanParamFactsOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectStoreScanParamFactsOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("noStorageOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectNoStorageOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("iterBinderElementOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectIterBinderElementOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("exprFactsOverlay")).IsNil() {
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectExprFactsOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	if w.Kind() == reflect.Struct && w.Type() == reflect.TypeFor[Analyzer]() && !settable(w.FieldByName("exprTypeOverlay")).IsNil() {
		// A construction view cannot be merged into canonical analysis. Its
		// base is immutable and private tombstones are not canonical deltas.
		target := dst.Addr().Interface().(*Analyzer)
		err := target.rejectExprTypeOverlayOperation("canonical worker merge")
		if p != nil && p.specialization != nil {
			p.specialization.err = err
		}
		return
	}
	switch w.Kind() {
	case reflect.Struct:
		for i := 0; i < w.NumField(); i++ {
			if w.Type() == reflect.TypeOf(Analyzer{}) && (w.Type().Field(i).Name == "proofLifecycle" || w.Type().Field(i).Name == "proofReport") {
				continue
			}
			if p != nil {
				name := w.Type().Field(i).Name
				if name == "exprTypeOverlay" || name == "exprFactsOverlay" || name == "iterBinderElementOverlay" || name == "noStorageOverlay" || name == "optimizationSymbolIdentityOverlay" || name == "storeScanBinderOverlay" || name == "storeScanParamFactsOverlay" || parallelBodyWorkerLocalOnlyMap(name) || name == "smtSolver" || name == "smtStats" || name == "smtUnavailable" {
					continue
				}
			}
			mergeValue(settable(dst.Field(i)), settable(w.Field(i)), settable(base.Field(i)), p)
		}
	case reflect.Map:
		if w.IsNil() {
			return
		}
		if dst.IsNil() {
			dst.Set(reflect.MakeMap(w.Type()))
		}
		iter := w.MapRange()
		for iter.Next() {
			k, v := iter.Key(), iter.Value()
			if !p.ownsKey(k) {
				continue
			}
			var bv reflect.Value
			if !base.IsNil() {
				bv = base.MapIndex(k)
			}
			if v.Kind() == reflect.Map && !v.IsNil() {
				if dv := dst.MapIndex(k); dv.IsValid() && !dv.IsNil() {
					if !bv.IsValid() {
						bv = reflect.Zero(v.Type())
					}
					mergeValue(dv, v, bv, p)
					continue
				}
			}
			if bv.IsValid() && sameValue(bv, v) {
				continue
			}
			dst.SetMapIndex(k, v)
		}
	case reflect.Slice:
		if w.Len() > base.Len() && (base.Len() == 0 || w.Pointer() == base.Pointer()) {
			dst.Set(reflect.AppendSlice(dst, w.Slice(base.Len(), w.Len())))
		} else if w.Len() > base.Len() || (w.Pointer() != base.Pointer() && w.Len() != base.Len()) {
			dst.Set(w)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		// Counters: every worker's increments survive.
		dst.SetInt(dst.Int() + w.Int() - base.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		dst.SetUint(dst.Uint() + w.Uint() - base.Uint())
	default:
		if !sameValue(base, w) {
			dst.Set(w)
		}
	}
}

// mergeRequiresReport folds a worker's requires-report aggregation into a's: the
// entries are per-worker pointers keyed by clause, so equal keys are summed. The
// worker's copy is reset to the base so the generic merge leaves it alone.
func mergeRequiresReport(a, w *Analyzer, base reflect.Value) {
	for _, key := range w.requiresReportOrder {
		entry := w.requiresReportData[key]
		if entry == nil {
			continue
		}
		if a.requiresReportData == nil {
			a.requiresReportData = map[requiresReportKey]*RequiresReportEntry{}
		}
		have := a.requiresReportData[key]
		switch {
		case have == nil:
			copied := *entry
			a.requiresReportData[key] = &copied
			a.requiresReportOrder = append(a.requiresReportOrder, key)
		case have != entry:
			have.Provable += entry.Provable
			have.Unprovable += entry.Unprovable
			have.UnprovableSites = append(have.UnprovableSites, entry.UnprovableSites...)
		}
	}
	b := base.Interface().(Analyzer)
	w.requiresReportData, w.requiresReportOrder = b.requiresReportData, b.requiresReportOrder
}

// sameValue reports whether a worker left a value unchanged: equal for
// comparable values, same identity for maps, slices and funcs.
func sameValue(x, y reflect.Value) (same bool) {
	switch x.Kind() {
	case reflect.Map, reflect.Slice:
		return x.Pointer() == y.Pointer() && x.Len() == y.Len()
	case reflect.Func, reflect.UnsafePointer, reflect.Chan:
		return x.Pointer() == y.Pointer()
	}
	if !x.Type().Comparable() {
		return false
	}
	// An interface holding an incomparable dynamic value panics on ==.
	defer func() {
		if recover() != nil {
			same = false
		}
	}()
	return x.Interface() == y.Interface()
}

// precomputeSharedCalleeFacts settles recursive summaries before the fork, since
// mutually recursive bodies cannot be ordered callee-first. The fill-may-adopt set
// and sentinel names scan the whole file and are also prepared before workers start.
func (a *Analyzer) precomputeSharedCalleeFacts(bodies []scopedDecl, cyclic map[*ast.FuncDecl]bool) {
	t0 := time.Now()
	if a.fillMayAdoptSet == nil {
		a.fillMayAdoptSet = a.computeFillMayAdopt()
	}
	t1 := time.Now()
	// Whole-program body scans read every body; workers rewrite the bodies they own.
	a.sentinelFuncNames()
	if parallelReport && a.parallel == nil {
		defer func(t2 time.Time) {
			fmt.Fprintf(os.Stderr, "[parallel-bodies] precompute: fillMayAdopt %v, sentinel %v, infer %d bodies %v\n", t1.Sub(t0).Round(time.Millisecond), t2.Sub(t1).Round(time.Millisecond), len(bodies), time.Since(t2).Round(time.Millisecond))
		}(time.Now())
	}
	infer := func(fn *ast.FuncDecl) {
		if !cyclic[fn] {
			return
		}
		sym := a.funcDeclSymbols[fn]
		if sym == nil || fn.IsContract {
			return
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok {
			return
		}
		if len(fn.GenericParams) == 0 {
			a.inferFuncReturnProvenance(fn, fnType)
			a.inferFuncReturnBorrowedOwnerRefs(fn, fnType)
		}
		if !fnType.SinkParamsKnown {
			a.inferFuncSinkParams(fn, fnType)
		}
	}
	needsInference := func(fn *ast.FuncDecl) bool {
		if fn == nil || !cyclic[fn] || fn.IsContract {
			return false
		}
		sym := a.funcDeclSymbols[fn]
		if sym == nil {
			return false
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok || fnType == nil {
			return false
		}
		if len(fn.GenericParams) == 0 && !a.scalarReturnHasNoReferenceProvenance(fnType) {
			if !fnType.ReturnProvenanceKnown || !fnType.ReturnBorrowedOwnerRefsKnown {
				return true
			}
		}
		if !fnType.SinkParamsKnown {
			for _, param := range fnType.Params {
				if containsTypeParam(param) || a.containsAffineHandleValues(param, map[string]bool{}) {
					return true
				}
			}
		}
		return false
	}
	for _, scoped := range bodies {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			if !needsInference(n) {
				continue
			}
		case *ast.ImplDecl:
			if n.IsHandler {
				continue
			}
			// needsInference treats type parameters as unknown and conservatively
			// keeps them, so it is safe to avoid entering generic context for an
			// impl whose methods already have every required summary.
			needed := false
			for _, member := range n.Members {
				if fn, ok := member.(*ast.FuncDecl); ok && needsInference(fn) {
					needed = true
					break
				}
			}
			if !needed {
				continue
			}
		default:
			continue
		}
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.FuncDecl:
				infer(n)
			case *ast.ImplDecl:
				// The same Self and generic binding analyzeScopedDecl gives impl members.
				if n.IsHandler {
					return
				}
				a.withGenericParams(n.GenericParams, nil, func() {
					var selfBindings map[string]Type
					if receiver := a.resolveType(n.ForType); receiver != nil && !IsInvalidType(receiver) {
						selfBindings = map[string]Type{staticInterfaceSelfName: receiver}
					}
					a.withInterfaceAssocTypes(selfBindings, func() {
						for _, member := range n.Members {
							if fn, ok := member.(*ast.FuncDecl); ok && needsInference(fn) {
								infer(fn)
							}
						}
					})
				})
			}
		})
	}
}

// isolateWorkerScopes gives a worker its own global scope. Function scopes hang
// off it, and flow facts (range facts, SMT assert facts) are saved and restored on
// every scope of the chain, the global one included. Scratch pools that hand out
// pointers must not be shared either.
func isolateWorkerScopes(a, w *Analyzer) {
	if a.globalScope != nil {
		g := new(Scope)
		settable(reflect.ValueOf(g).Elem()).Set(deepCloneValue(reflect.ValueOf(a.globalScope).Elem()))
		w.globalScope = g
		if a.currentScope == a.globalScope {
			w.currentScope = g
		}
	}
	w.smtTextFree = nil
}

var parallelReport = os.Getenv("ELISA_PARALLEL_GRAPH_REPORT") != ""
