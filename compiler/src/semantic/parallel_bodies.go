package semantic

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"elisacore/src/ast"
)

// parallelBodyWorkers reads ELISA_PARALLEL_BODIES: the number of workers that
// analyze function bodies concurrently. Unset or <= 1 keeps the serial path.
func parallelBodyWorkers() int {
	n, err := strconv.Atoi(os.Getenv("ELISA_PARALLEL_BODIES"))
	if err != nil {
		return 0
	}
	return n
}

// analyzeDeclsParallel analyzes non-function declarations serially, then splits
// the function-bearing declarations into contiguous chunks in source order. Each
// chunk runs on its own copy of the Analyzer; the copies' state is merged back in
// chunk order, so diagnostics keep source order.
func (a *Analyzer) analyzeDeclsParallel(decls []scopedDecl, workers int) {
	var bodies []scopedDecl
	for _, scoped := range decls {
		switch scoped.Decl.(type) {
		case *ast.FuncDecl, *ast.ImplDecl:
			bodies = append(bodies, scoped)
		default:
			a.analyzeScopedDecl(scoped)
		}
	}
	if len(bodies) < workers {
		workers = len(bodies)
	}

	t0 := time.Now()
	a.precomputeSharedCalleeFacts(bodies)
	tp := time.Now()
	base := deepCloneValue(reflect.ValueOf(a).Elem())
	clones := make([]*Analyzer, workers)
	for i := range clones {
		w := new(Analyzer)
		settable(reflect.ValueOf(w).Elem()).Set(deepCloneValue(reflect.ValueOf(a).Elem()))
		clones[i] = w
	}
	baseDiagnostics := len(a.diagnostics)
	// Small blocks dealt round-robin balance the expensive stretches of a file
	// across workers while keeping the assignment independent of timing.
	const block = 8
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
			for first := i * block; first < len(bodies); first += workers * block {
				for d := first; d < first+block && d < len(bodies); d++ {
					from := len(w.diagnostics)
					w.analyzeScopedDecl(bodies[d])
					spans[d] = span{worker: i, from: from, to: len(w.diagnostics)}
					n++
				}
			}
			fmt.Fprintf(os.Stderr, "[parallel-bodies] worker %d: %d decls %v\n", i, n, time.Since(ts).Round(time.Millisecond))
		}(clones[i], i)
	}
	wg.Wait()
	t2 := time.Now()
	workerDiags := make([][]Diagnostic, workers)
	for i, w := range clones {
		workerDiags[i] = w.diagnostics
		w.diagnostics = a.diagnostics[:baseDiagnostics:baseDiagnostics]
		w.reportedDiagnostics = a.reportedDiagnostics
		mergeValue(settable(reflect.ValueOf(a).Elem()), reflect.ValueOf(w).Elem(), base)
	}
	a.diagnostics = a.diagnostics[:baseDiagnostics]
	for _, s := range spans {
		for _, d := range workerDiags[s.worker][s.from:s.to] {
			a.appendDiagnostic(d)
		}
	}
	fmt.Fprintf(os.Stderr, "[parallel-bodies] precompute %v, clone %v, bodies %v, merge %v\n", tp.Sub(t0).Round(time.Millisecond), t1.Sub(tp).Round(time.Millisecond), t2.Sub(t1).Round(time.Millisecond), time.Since(t2).Round(time.Millisecond))
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
func mergeValue(dst, w, base reflect.Value) {
	switch w.Kind() {
	case reflect.Struct:
		for i := 0; i < w.NumField(); i++ {
			mergeValue(settable(dst.Field(i)), settable(w.Field(i)), settable(base.Field(i)))
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
			var bv reflect.Value
			if !base.IsNil() {
				bv = base.MapIndex(k)
			}
			if v.Kind() == reflect.Map && !v.IsNil() {
				if dv := dst.MapIndex(k); dv.IsValid() && !dv.IsNil() {
					if !bv.IsValid() {
						bv = reflect.Zero(v.Type())
					}
					mergeValue(dv, v, bv)
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
	default:
		if !sameValue(base, w) {
			dst.Set(w)
		}
	}
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
	infer := func(fn *ast.FuncDecl) {
		sym := a.funcDeclSymbols[fn]
		if sym == nil || fn.IsContract || len(fn.GenericParams) != 0 {
			return
		}
		fnType, ok := sym.Type.(*FuncType)
		if !ok {
			return
		}
		a.inferFuncReturnProvenance(fn, fnType)
		a.inferFuncReturnBorrowedOwnerRefs(fn, fnType)
	}
	for _, scoped := range bodies {
		a.withResolutionContext(scoped.Namespace, scoped.Usings, func() {
			switch n := scoped.Decl.(type) {
			case *ast.FuncDecl:
				infer(n)
			}
		})
	}
}
