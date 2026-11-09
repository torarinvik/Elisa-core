package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"math"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
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
	// performance-core workers down more than they add. Keep one worker per P core;
	// the extra worker increased map-copy and GC pressure on the Stage1 seed workload.
	if p := performanceCores(); p > 0 {
		return min(p, runtime.NumCPU())
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
		if snap, err := ast.NewTransientSyntaxSnapshot(&ast.File{Decls: []ast.Decl{scoped.Decl}}, 1<<40); err == nil {
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
