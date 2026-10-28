package semantic

import (
	"elisacore/src/ast"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedCodegenMapCopiesIsolateAndRestore(t *testing.T) {
	node := &ast.Ident{Name: "value"}
	helperCall := &ast.CallExpr{}
	oldType := &BuiltinType{Name: "i64"}
	original := map[ast.Expr]Type{node: oldType}
	private := map[string]bool{"private": true}
	helperCalls := map[*ast.CallExpr]string{helperCall: "clone"}
	a := &Analyzer{exprTypes: original, programRegions: private, compilerBuiltinHelperCalls: helperCalls}
	r := &Result{ExprTypes: original, analyzer: a}
	r.BeginSharedCodegen()
	if !r.IsProgramRegion("private") || !r.IsCompilerBuiltinHelperCall(helperCall, "clone") {
		t.Fatal("shared codegen snapshots missed analyzed query rows")
	}
	replacement := &BuiltinType{Name: "bool"}
	a.exprTypes[node] = replacement
	added := &ast.Ident{Name: "added"}
	a.exprTypes[added] = replacement
	if r.ExprTypes[node] != oldType || r.ExprTypes[added] != nil {
		t.Fatal("analyzer writes leaked to shared result")
	}
	a.programRegions["same-private-map"] = true
	if !private["same-private-map"] {
		t.Fatal("unshared map was copied")
	}
	a.programRegions["private"] = false
	a.compilerBuiltinHelperCalls[helperCall] = "mutated"
	if !r.IsProgramRegion("private") || r.IsProgramRegion("same-private-map") || !r.IsCompilerBuiltinHelperCall(helperCall, "clone") || r.IsCompilerBuiltinHelperCall(helperCall, "mutated") {
		t.Fatal("shared codegen queries did not retain their immutable snapshots")
	}
	r.EndSharedCodegen()
	if a.exprTypes[node] != oldType || a.exprTypes[added] != nil {
		t.Fatal("original analyzer map was not restored")
	}
	a.exprTypes[added] = oldType
	if r.ExprTypes[added] != oldType {
		t.Fatal("restored map lost identity")
	}
}

func TestSharedCodegenAnalyzerQuerySnapshotsAreConcurrent(t *testing.T) {
	call := &ast.CallExpr{}
	r := &Result{analyzer: &Analyzer{
		compilerBuiltinHelperCalls: map[*ast.CallExpr]string{call: "clone"},
		programRegions:             map[string]bool{"program": true},
	}}
	r.BeginSharedCodegen()
	defer r.EndSharedCodegen()

	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 1000; j++ {
				if !r.IsProgramRegion("program") || !r.IsCompilerBuiltinHelperCall(call, "clone") {
					t.Error("shared analyzer query snapshot lost an immutable row")
					return
				}
			}
		}()
	}
	workers.Wait()
}

func TestSharedCodegenSpecializationMemoDeduplicatesSameKey(t *testing.T) {
	fn := &ast.FuncDecl{}
	key := specializationKey{fn: fn, args: "i64"}
	product := &SpecializedFunction{Decl: fn}
	shared := &sharedCodegen{bodies: make(map[specializationKey]*SpecializedFunction)}
	var builds atomic.Int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	results := make([]*SpecializedFunction, 8)
	errors := make([]error, len(results))
	for i := range results {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			results[i], errors[i] = shared.specialize(key, func() (*SpecializedFunction, error) {
				builds.Add(1)
				time.Sleep(10 * time.Millisecond)
				return product, nil
			})
		}(i)
	}
	close(start)
	workers.Wait()
	if got := builds.Load(); got != 1 {
		t.Fatalf("same specialization key built %d times, want 1", got)
	}
	for i, result := range results {
		if errors[i] != nil || result != product {
			t.Fatalf("worker %d got product %p, error %v; want shared product %p", i, result, errors[i], product)
		}
	}
}

func TestSharedCodegenSpecializationMemoDoesNotRetainUncacheableProduct(t *testing.T) {
	fn := &ast.FuncDecl{}
	key := specializationKey{fn: fn, args: "i64"}
	shared := &sharedCodegen{bodies: make(map[specializationKey]*SpecializedFunction)}
	transient := &SpecializedFunction{Decl: fn, uncacheableSMTOutcome: true}
	if product, err := shared.specialize(key, func() (*SpecializedFunction, error) {
		return transient, nil
	}); product != transient || err != nil {
		t.Fatalf("transient specialization returned product %p and error %v", product, err)
	}
	want := &SpecializedFunction{Decl: fn}
	var builds atomic.Int32
	if product, err := shared.specialize(key, func() (*SpecializedFunction, error) {
		builds.Add(1)
		return want, nil
	}); product != want || err != nil {
		t.Fatalf("retry returned product %p and error %v, want %p", product, err, want)
	}
	if builds.Load() != 1 {
		t.Fatalf("retry builder ran %d times, want 1", builds.Load())
	}
}

func TestSharedCodegenSpecializationMemoRunsDistinctKeysConcurrently(t *testing.T) {
	shared := &sharedCodegen{bodies: make(map[specializationKey]*SpecializedFunction)}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		fn := &ast.FuncDecl{}
		key := specializationKey{fn: fn, args: "i64"}
		workers.Add(1)
		go func(key specializationKey) {
			defer workers.Done()
			_, err := shared.specialize(key, func() (*SpecializedFunction, error) {
				started <- struct{}{}
				<-release
				return &SpecializedFunction{Decl: key.fn}, nil
			})
			if err != nil {
				t.Errorf("specialize: %v", err)
			}
		}(key)
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			workers.Wait()
			t.Fatal("distinct specialization keys did not start concurrently")
		}
	}
	close(release)
	workers.Wait()
}

func TestSharedCodegenSpecializationMemoRetriesErrorsAndPanics(t *testing.T) {
	fn := &ast.FuncDecl{}
	key := specializationKey{fn: fn, args: "i64"}
	shared := &sharedCodegen{bodies: make(map[specializationKey]*SpecializedFunction)}
	failure := errors.New("specialization failed")
	if product, err := shared.specialize(key, func() (*SpecializedFunction, error) {
		return nil, failure
	}); product != nil || !errors.Is(err, failure) {
		t.Fatalf("failed specialization returned product %p and error %v", product, err)
	}
	want := &SpecializedFunction{Decl: fn}
	if product, err := shared.specialize(key, func() (*SpecializedFunction, error) {
		return want, nil
	}); product != want || err != nil {
		t.Fatalf("retry returned product %p and error %v, want %p", product, err, want)
	}
	if product, err := shared.specialize(key, func() (*SpecializedFunction, error) {
		t.Fatal("successful specialization was rebuilt")
		return nil, nil
	}); product != want || err != nil {
		t.Fatalf("memo hit returned product %p and error %v, want %p", product, err, want)
	}

	panicKey := specializationKey{fn: &ast.FuncDecl{}, args: "bool"}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("specialization panic was swallowed")
			}
		}()
		_, _ = shared.specialize(panicKey, func() (*SpecializedFunction, error) {
			panic("builder panic")
		})
	}()
	if _, err := shared.specialize(panicKey, func() (*SpecializedFunction, error) {
		return &SpecializedFunction{Decl: panicKey.fn}, nil
	}); err != nil {
		t.Fatalf("retry after panic failed: %v", err)
	}
}

func BenchmarkSharedCodegenMapCopies(b *testing.B) {
	original := make(map[ast.Expr]Type, 10000)
	typ := &BuiltinType{Name: "i64"}
	for i := 0; i < 10000; i++ {
		original[&ast.Ident{Name: "value"}] = typ
	}
	a := &Analyzer{exprTypes: original}
	r := &Result{ExprTypes: original, analyzer: a}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.BeginSharedCodegen()
		r.EndSharedCodegen()
	}
}
