package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Store-capturing submission (docs/74 implicit stores x threads).
//
// A function that reads a region-backed packed enum carries the enum's store as a hidden trailing
// parameter (`__packed_store_E`). Source code cannot name that parameter, and `pool_submit1`'s
// `fn(A) -> R` cannot carry it, so `submit worker(x)` of such a function used to fail with
// `expects fn(A) with __packed_store_E: <invalid> -> R`. The submission now CAPTURES the
// submitting scope's store instead: the call type-checks against the worker's explicit signature,
// and the backend boxes `{arg, store...}` into the work item and submits a per-site entry thunk that
// calls `worker(arg, store...)` (backend/llvm_submit_store_capture.go).
//
// The store is shared with the worker thread, so the worker may only READ it: a worker that builds
// nodes of the hierarchy (directly or through any function it calls) would grow the store while
// other threads read it, so it is rejected here. The submitting thread must not build into the store
// either until the task is joined (a nursery joins at scope exit); that half is the caller's
// contract, as for any value shared with a nursery task.

// SubmitStoreCaptureNames returns the implicit packed-store parameter names that a function value
// passed to pool_submit1 carries beyond its explicit signature, or nil when it carries none or carries
// any other implicit parameter (a hidden region, a tree store), which stays unsupported.
func SubmitStoreCaptureNames(fn *FuncType) []string {
	if fn == nil || len(fn.ImplicitParamNames) == 0 {
		return nil
	}
	// Implicit parameters trail the explicit ones.
	if len(fn.ImplicitParamNames) > len(fn.Params) {
		return nil
	}
	for _, name := range fn.ImplicitParamNames {
		if !strings.HasPrefix(name, "__packed_store_") || strings.HasPrefix(name, PackedStoreImplicitUsePrefix) {
			return nil
		}
	}
	return append([]string(nil), fn.ImplicitParamNames...)
}

// stripImplicitParams returns a copy of fn with only its first `explicit` parameters.
func stripImplicitParams(fn *FuncType, explicit int) *FuncType {
	if fn == nil {
		return nil
	}
	clone := *fn
	if explicit > len(fn.Params) {
		explicit = len(fn.Params)
	}
	clone.Params = append([]Type(nil), fn.Params[:explicit]...)
	clone.ImplicitParamNames = nil
	clone.ExplicitParamCount = 0
	if len(clone.ExplicitParamNames) > explicit {
		clone.ExplicitParamNames = clone.ExplicitParamNames[:explicit]
	}
	return &clone
}

// acceptStoreCapturingSubmit checks the worker argument of a `pool_submit1` call whose function
// value carries implicit packed-store parameters. It returns the expected parameter type to
// instantiate pool_submit1 with (the worker's explicit signature) and true when the submission is
// a store capture; false leaves the ordinary assignability diagnostic in charge.
func (a *Analyzer) acceptStoreCapturingSubmit(callee string, index int, expected, actual Type) (Type, bool) {
	if callee != "pool_submit1" || index != 1 {
		return nil, false
	}
	actualFunc, ok := actual.(*FuncType)
	if !ok {
		return nil, false
	}
	names := SubmitStoreCaptureNames(actualFunc)
	if len(names) == 0 {
		return nil, false
	}
	expectedFunc, ok := expected.(*FuncType)
	if !ok {
		return nil, false
	}
	explicit := len(actualFunc.Params) - len(names)
	stripped := stripImplicitParams(actualFunc, explicit)
	expectedStripped := stripImplicitParams(expectedFunc, explicit)
	if !AssignableTo(expectedStripped, stripped) {
		return nil, false
	}
	return expectedStripped, true
}

// checkStoreCapturingSubmitWorker rejects a store-capturing worker that builds nodes of a captured
// hierarchy: the store would grow on the worker thread while the submitter and sibling tasks read it.
// The diagnostic names the function that constructs the nodes.
func (a *Analyzer) checkStoreCapturingSubmitWorker(arg ast.Expr, worker *FuncType) {
	if a.packedStoreBuilders == nil {
		return
	}
	captured := map[string]bool{}
	for _, name := range SubmitStoreCaptureNames(worker) {
		captured[strings.TrimPrefix(name, "__packed_store_")] = true
	}
	roots := make([]string, 0)
	for root := range a.packedStoreBuilders[worker] {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for _, root := range roots {
		if !captured[sanitizeImplicitTempBase(root)] {
			continue
		}
		witness := a.packedStoreBuilders[worker][root]
		via := ""
		if witness != "" && witness != worker.Name {
			via = fmt.Sprintf(" (through %q)", witness)
		}
		a.errorf(arg.Pos(), "%q is submitted to another thread and shares the submitting scope's %s store, but it builds %s nodes%s; a submitted worker may only read a captured store", worker.Name, ast.ModulePathSpelling(root), ast.ModulePathSpelling(root), via)
	}
}

// funcBarePackedVariantRoots returns the hierarchy roots of the payload-less packed variants a
// function uses as values (`Expr.Absent`): such a value is a fresh node allocated in the store
// just like a constructor call, so a store-capturing worker must not produce one either.
func (a *Analyzer) funcBarePackedVariantRoots(fn *ast.FuncDecl) map[string]bool {
	out := map[string]bool{}
	if fn == nil {
		return out
	}
	var rec func(v reflect.Value)
	rec = func(v reflect.Value) {
		if !v.IsValid() || !v.CanInterface() {
			return
		}
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() {
				return
			}
			if field, ok := v.Interface().(*ast.FieldExpr); ok && field != nil {
				if enumType, variant, ok := a.enumConstructorInfoFromFieldExpr(field); ok && enumType != nil && variant != nil && enumType.Packed && len(variant.Payload) == 0 {
					out[enumType.Root().Name] = true
				}
			}
			rec(v.Elem())
		case reflect.Interface:
			if v.IsNil() {
				return
			}
			rec(v.Elem())
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				rec(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				rec(v.Index(i))
			}
		}
	}
	rec(reflect.ValueOf(fn.Body))
	return out
}
