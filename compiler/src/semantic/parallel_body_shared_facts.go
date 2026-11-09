package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"os"
	"reflect"
	"time"
)

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
