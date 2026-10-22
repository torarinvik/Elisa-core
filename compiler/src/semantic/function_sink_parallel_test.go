package semantic

import (
	"sync/atomic"
	"testing"

	"elisacore/src/ast"
)

func TestParallelWorkersKeepBodylessSinkSummariesPrivate(t *testing.T) {
	shared := &FuncType{
		Name:   "ctx_string_slices_eq",
		Params: []Type{&BuiltinType{Name: "cstr"}},
	}
	scope := NewScope(nil)
	scope.Define(&Symbol{Name: shared.Name, Kind: SymbolFunc, Type: shared})
	callee := &ast.Ident{Name: shared.Name}
	workers := []*Analyzer{
		{globalScope: scope, parallel: &parallelWorkerState{worker: 0}},
		{globalScope: scope, parallel: &parallelWorkerState{worker: 1}},
	}
	private := make([]*FuncType, len(workers))
	for i, worker := range workers {
		_, fnType, ok := worker.resolveSinkFuncDecl(callee)
		if !ok || fnType == nil {
			t.Fatal("bodyless function type did not resolve")
		}
		private[i] = fnType
	}
	if private[0] == shared || private[1] == shared || private[0] == private[1] {
		t.Fatal("parallel workers share a mutable sink-summary FuncType")
	}

	private[0].SinkParams = []bool{true}
	private[0].SinkParamsKnown = true
	if !workers[0].funcParamAllowsImplicitSink(callee, shared, 0) {
		t.Fatal("worker-local sink summary was not used for the call")
	}
	if workers[1].funcParamAllowsImplicitSink(callee, shared, 0) {
		t.Fatal("one worker's sink summary leaked into another worker")
	}
	if shared.SinkParamsKnown || len(shared.SinkParams) != 0 {
		t.Fatal("worker-local sink inference mutated the shared declaration type")
	}
}

func TestParallelFuncTypeCachePromotesOwnershipAfterClaim(t *testing.T) {
	decl := &ast.FuncDecl{Name: "work"}
	shared := &FuncType{Name: "work"}
	p := &parallelWorkerState{
		worker: 0,
		at:     map[*ast.FuncDecl][2]int{decl: {0, -1}},
		slots:  make([]atomic.Int32, 1),
	}
	p.slots[0].Store(-1)
	worker := &Analyzer{parallel: p}

	beforeClaim := worker.privateFuncType(decl, shared)
	if beforeClaim == shared || p.funcTypeOwned[shared] {
		t.Fatal("pre-claim read should cache a private, unowned function type")
	}
	p.slots[0].Store(0)
	afterClaim := worker.privateFuncType(decl, shared)
	if afterClaim != beforeClaim {
		t.Fatal("ownership promotion replaced the cached worker-local function type")
	}
	if !p.funcTypeOwned[shared] {
		t.Fatal("cached function type did not become publishable after this worker claimed its body")
	}

	afterClaim.SinkParams = []bool{true}
	afterClaim.SinkParamsKnown = true
	p.publishFuncTypes()
	if !shared.SinkParamsKnown || len(shared.SinkParams) != 1 || !shared.SinkParams[0] {
		t.Fatal("claimed worker did not publish facts through its cached function type")
	}
}
