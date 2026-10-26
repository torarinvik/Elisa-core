//go:build cgo

package semantic

import (
	"runtime"
	"sync"
	"testing"

	"elisacore/src/ast"
)

// The reader represents backend walkers which traverse canonical syntax while
// other workers build private specialization views. Equal pointer assignments
// still constitute writes.
func TestSharedSpecializationKeepsTemplateSyntaxReadOnly(t *testing.T) {
	result := analyzeTreeTestSource(t, "shared_specialization_ast.elisa", `def bounce[T](value: T, depth: i64) -> T:
    return value

def first[T](value: T) -> T:
    return bounce(value, 0)
`)
	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "first" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("missing generic first")
	}
	ret, ok := fn.Body[0].(*ast.ReturnStmt)
	if !ok {
		t.Fatal("missing return")
	}
	call, ok := ret.Value.(*ast.CallExpr)
	if !ok || len(call.ResolvedArgs) == 0 {
		t.Fatal("missing resolved call arguments")
	}
	stop, ready := make(chan struct{}), make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	go func() {
		defer readers.Done()
		close(ready)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for _, arg := range call.ResolvedArgs {
				runtime.KeepAlive(arg)
			}
			for _, arg := range call.Args {
				runtime.KeepAlive(arg)
			}
			runtime.Gosched()
		}
	}()
	<-ready
	defer func() { close(stop); readers.Wait() }()
	// Each session has a fresh memo, like independent object-emission sessions.
	// Only the public specialization API writes analyzer state in this fixture.
	for i := 0; i < 32; i++ {
		result.BeginSharedCodegen()
		overlay := result.SpecializedExprTypes(fn, []Type{result.analyzer.namedTypes["i64"]})
		result.EndSharedCodegen()
		if len(overlay) == 0 {
			t.Fatal("fixture did not specialize")
		}
	}
}

func TestSharedSpecializationBuildsDistinctKeysConcurrently(t *testing.T) {
	result := analyzeTreeTestSource(t, "shared_specialization_keys.elisa", `struct Left:
    value: i64

struct Right:
    value: i64

def identity[T](value: T) -> T:
    return value
`)
	var fn *ast.FuncDecl
	for _, decl := range result.File.Decls {
		if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name == "identity" {
			fn = candidate
			break
		}
	}
	if fn == nil {
		t.Fatal("missing generic identity")
	}
	arguments := []Type{result.NamedTypes["Left"], result.NamedTypes["Right"]}
	for _, argument := range arguments {
		if argument == nil {
			t.Fatal("missing concrete type argument")
		}
	}
	result.BeginSharedCodegen()
	defer result.EndSharedCodegen()

	start := make(chan struct{})
	products := make([]*SpecializedFunction, len(arguments))
	errors := make([]error, len(arguments))
	var workers sync.WaitGroup
	for i, argument := range arguments {
		workers.Add(1)
		go func(i int, argument Type) {
			defer workers.Done()
			<-start
			products[i], errors[i] = result.SpecializeFunction(fn, []Type{argument})
		}(i, argument)
	}
	close(start)
	workers.Wait()
	for i, product := range products {
		if errors[i] != nil || product == nil {
			t.Fatalf("specialization %d failed: product %p, error %v", i, product, errors[i])
		}
		ret, ok := product.Decl.Body[0].(*ast.ReturnStmt)
		if !ok || product.Result.ValueExprTypes(ret.Value) != arguments[i] {
			t.Fatalf("specialization %d did not retain its concrete return type", i)
		}
	}
}
