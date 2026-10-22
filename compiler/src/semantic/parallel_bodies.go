package semantic

import (
	"fmt"
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
			shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{},
			predicateMemoBases: parallelPredicateMemoBases{
				affineHandles: a.affineHandleMemo, borrowedOwnerRefs: a.borrowedOwnerRefMemo,
				regionRefs: a.regionRefsMemo, mutableBorrowCapabilities: a.mutableBorrowCapabilityMemo,
				storeScanAddresses: a.storeScanAddressMemo,
			},
			programLivedTypesBase: a.programLivedTypes, smtLocalDefDepsBase: a.smtLocalDefDeps,
			resolvedTypeNamesBase: a.resolvedTypeNames, abstractParamRegionRefMemoBase: a.abstractParamRegionRefMemo,
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
			ts := time.Now()
			n := 0
			var waited, blocked time.Duration
			for {
				u := sched.claim(i)
				if u < 0 {
					break
				}
				for _, d := range sched.units[u] {
					slots[d].Store(int32(i))
				}
				tw := time.Now()
				for _, du := range sched.deps[u] {
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
	for i, w := range clones {
		workerDiags[i] = w.diagnostics
		w.diagnostics = a.diagnostics[:baseDiagnostics:baseDiagnostics]
		w.reportedDiagnostics = a.reportedDiagnostics
		w.globalScope, w.currentScope, w.smtTextFree = a.globalScope, a.currentScope, a.smtTextFree
		p := w.parallel
		w.parallel = nil
		mergeRequiresReport(a, w, base)
		mergeValue(settable(reflect.ValueOf(a).Elem()), reflect.ValueOf(w).Elem(), base, p)
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
	a.diagnostics = a.diagnostics[:baseDiagnostics]
	for _, s := range spans {
		for _, d := range workerDiags[s.worker][s.from:s.to] {
			a.appendDiagnostic(d)
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
	if a == nil {
		return reflect.Value{}
	}
	source := reflect.ValueOf(a).Elem()
	cloned := reflect.New(source.Type()).Elem()
	cloned.Set(source)
	shareDeclarationMaps := a.retained == nil
	for i := 0; i < source.NumField(); i++ {
		field := source.Field(i)
		name := source.Type().Field(i).Name
		if useMemoOverlays && name == "storeFlowSummaries" {
			settable(cloned.Field(i)).Set(reflect.ValueOf(cloneStoreFlowSummariesForBodyWorker(a.storeFlowSummaries)))
			continue
		}
		// These caches contain lazily populated maps/slices behind pointers.
		// Rebuild them privately rather than sharing mutable pointer values.
		if useMemoOverlays && parallelBodyWorkerRebuildMap(name) {
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if useMemoOverlays && a.retained == nil && parallelBodyMemoMap(name) {
			settable(cloned.Field(i)).Set(reflect.Zero(field.Type()))
			continue
		}
		if shareDeclarationMaps && field.Kind() == reflect.Map && !field.IsNil() && parallelDeclarationMapIsReadOnly(name) {
			continue
		}
		settable(cloned.Field(i)).Set(deepCloneValue(settable(field)))
	}
	return cloned
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
	default:
		return false
	}
}

func parallelBodyMemoMap(field string) bool {
	switch field {
	case "affineHandleMemo", "borrowedOwnerRefMemo", "regionRefsMemo", "mutableBorrowCapabilityMemo", "storeScanAddressMemo",
		"programLivedTypes", "smtLocalDefDeps", "resolvedTypeNames", "abstractParamRegionRefMemo":
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
	switch w.Kind() {
	case reflect.Struct:
		for i := 0; i < w.NumField(); i++ {
			if p != nil && parallelBodyWorkerLocalOnlyMap(w.Type().Field(i).Name) {
				continue
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
