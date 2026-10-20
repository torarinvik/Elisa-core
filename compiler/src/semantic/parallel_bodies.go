package semantic

import (
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
	return min(runtime.NumCPU(), 8)
}

// analyzeDeclsParallel analyzes non-function declarations serially, then splits
// the function-bearing declarations into contiguous chunks in source order. Each
// chunk runs on its own copy of the Analyzer; the copies' state is merged back in
// chunk order, so diagnostics keep source order.
func (a *Analyzer) analyzeDeclsParallel(decls []scopedDecl, workers int) {
	var bodies []scopedDecl
	for _, scoped := range decls {
		switch n := scoped.Decl.(type) {
		case *ast.FuncDecl:
			// Laws are read through refinement types and aliases the call graph does
			// not see; they are small and pure, so they are settled before the fork.
			if n.IsLaw {
				a.analyzeScopedDecl(scoped)
				continue
			}
			bodies = append(bodies, scoped)
		case *ast.ImplDecl:
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
	// Callee-first, as the serial pass reaches each callee from its callers.
	var ordered []scopedDecl
	for _, unit := range sched.units {
		for _, b := range unit {
			ordered = append(ordered, bodies[b])
		}
	}
	a.precomputeSharedCalleeFacts(ordered)
	// Per-return element states are scratch for the body analysis that owns them; a
	// precompute pass that met a return must not leave a merged-into prior behind.
	a.returnElementStmtStates, a.returnElementStmtUnknown = nil, nil
	tp := time.Now()
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
	base := deepCloneValue(reflect.ValueOf(a).Elem())
	clones := make([]*Analyzer, workers)
	smtFlights := &sync.Map{}
	for i := range clones {
		w := new(Analyzer)
		settable(reflect.ValueOf(w).Elem()).Set(deepCloneValue(reflect.ValueOf(a).Elem()))
		isolateWorkerScopes(a, w)
		w.parallel = &parallelWorkerState{worker: i, snapshots: snapshots, frozen: frozen, at: at, slots: slots, smtFlights: smtFlights, shadows: map[*ast.FuncDecl]*ast.FuncDecl{}, original: map[*ast.FuncDecl]*ast.FuncDecl{}}
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
					fmt.Fprintf(os.Stderr, "[parallel-unit] worker %d unit %d: %d bodies %v (first %T)\n", i, u, len(sched.units[u]), el.Round(time.Millisecond), bodies[sched.units[u][0]].Decl)
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
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(iter.Key(), deepCloneValue(iter.Value()))
		}
		return out
	case reflect.Struct:
		if !typeHoldsMapInline(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			settable(out.Field(i)).Set(deepCloneValue(settable(out.Field(i))))
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

// precomputeSharedCalleeFacts settles, before the fork, the facts a body asks of
// OTHER functions on demand: return provenance and borrowed-owner refs analyze the
// callee's body, and the fill-may-adopt set walks the whole file. Computed lazily
// from a worker, each would walk a body another worker is rewriting.
func (a *Analyzer) precomputeSharedCalleeFacts(bodies []scopedDecl) {
	if a.fillMayAdoptSet == nil {
		a.fillMayAdoptSet = a.computeFillMayAdopt()
	}
	// Whole-program body scans read every body; workers rewrite the bodies they own.
	a.sentinelFuncNames()
	infer := func(fn *ast.FuncDecl) {
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
	for _, scoped := range bodies {
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
							if fn, ok := member.(*ast.FuncDecl); ok {
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
