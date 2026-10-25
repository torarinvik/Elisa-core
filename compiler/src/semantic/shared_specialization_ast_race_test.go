//go:build cgo

package semantic

import (
	"runtime"
	"sync"
	"testing"

	"elisacore/src/ast"
)

// The reader represents backend walkers which traverse canonical syntax without
// acquiring sharedCodegen.mu. Equal pointer assignments still constitute writes.
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
