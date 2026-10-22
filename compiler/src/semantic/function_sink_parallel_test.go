package semantic

import (
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
