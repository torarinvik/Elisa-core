package semantic

import (
	"reflect"
	"sync"
	"testing"

	"elisacore/src/ast"
)

// The real integration regression is TestSMTDeclinesFloatValueDict under -race:
// a sink query copies a shared FuncType while another unit publishes summaries.
// Exercise that boundary repeatedly without any solver or body-analysis cost.
func TestParallelFuncTypeCopySynchronizesWithPublication(t *testing.T) {
	var publication sync.Mutex
	shared := &FuncType{Name: "callee"}
	before := &FuncType{Name: "callee"}
	after := &FuncType{Name: "callee", SinkParamsKnown: true, SinkParams: []bool{true}, ReturnIsolationKnown: true}
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		for i := 0; i < 512; i++ {
			publication.Lock()
			if i%2 == 0 {
				publishChangedFields(shared, after)
			} else {
				publishChangedFields(shared, before)
			}
			publication.Unlock()
		}
	}()
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			worker := &Analyzer{parallel: &parallelWorkerState{funcTypeMu: &publication}}
			for j := 0; j < 128; j++ {
				worker.parallel.funcTypes = nil // force a new whole-type snapshot
				local := worker.privateFuncType(nil, shared)
				if local == shared || local.SinkParamsKnown != local.ReturnIsolationKnown || (local.SinkParamsKnown && (len(local.SinkParams) != 1 || !local.SinkParams[0])) {
					t.Error("private type snapshot observed a partial publication")
					return
				}
				applied := worker.substituteType(shared, nil, nil, nil, nil).(*FuncType)
				if applied.SinkParamsKnown != applied.ReturnIsolationKnown || (applied.SinkParamsKnown && (len(applied.SinkParams) != 1 || !applied.SinkParams[0])) {
					t.Error("function substitution observed a partial publication")
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
}

func TestParallelImportedFuncTypeReplacesProvisionalSnapshot(t *testing.T) {
	decl := &ast.FuncDecl{Name: "callee"}
	shared := &FuncType{Name: decl.Name}
	symbol := &Symbol{Name: decl.Name, Kind: SymbolFunc, Type: shared, Node: decl}
	scope := NewScope(nil)
	scope.Define(symbol)
	schedule := &bodySchedule{
		funcs: [][]*ast.FuncDecl{{decl}}, done: []chan struct{}{make(chan struct{})},
		exported: make([]map[int]reflect.Value, 1), nodes: make([][]reflect.Value, 1),
	}
	worker := &Analyzer{
		globalScope: scope, funcDeclSymbols: map[*ast.FuncDecl]*Symbol{decl: symbol},
		parallel: &parallelWorkerState{funcTypeMu: &schedule.mu},
	}
	provisional := worker.privateFuncType(decl, shared)
	provisional.SinkParamsKnown, provisional.SinkParams = true, []bool{false}
	finished := &FuncType{Name: decl.Name, SinkParamsKnown: true, SinkParams: []bool{true}, ReturnIsolationKnown: true, ReturnIsolation: ReturnIsolationSummary{Known: true, Isolated: true}}
	schedule.mu.Lock()
	publishChangedFields(shared, finished)
	schedule.mu.Unlock()
	close(schedule.done[0])
	schedule.importUnit(worker, 0)
	_, current, ok := worker.resolveSinkFuncDecl(&ast.Ident{Name: decl.Name})
	if !ok || current == provisional || current == shared || !current.SinkParamsKnown || len(current.SinkParams) != 1 || !current.SinkParams[0] || !current.ReturnIsolationKnown || !current.ReturnIsolation.Isolated {
		t.Fatal("imported finished facts were masked by an earlier private cache")
	}
	if !provisional.SinkParamsKnown || len(provisional.SinkParams) != 1 || provisional.SinkParams[0] || provisional.ReturnIsolationKnown {
		t.Fatal("import mutated an old private snapshot that other metadata may retain")
	}
	// A declaration's owner keeps its analysis-local state; only foreign
	// provisional copies are invalidated at dependency imports.
	worker.parallel.funcTypeOwned = map[*FuncType]bool{shared: true}
	worker.invalidateImportedFuncType(decl)
	if worker.parallel.funcTypes[shared] != current {
		t.Fatal("dependency invalidation discarded an owned type")
	}
}
