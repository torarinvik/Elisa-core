package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

// Independent pre-optimization implementation of the SCC traversal.
func oldRecursiveEdges(a *Analyzer, root *ast.FuncDecl) []recursionEdge {
	type resolved struct {
		call   *ast.CallExpr
		callee *ast.FuncDecl
	}
	calls := map[*ast.FuncDecl][]resolved{}
	var order []*ast.FuncDecl
	var walk func(*ast.FuncDecl)
	walk = func(fn *ast.FuncDecl) {
		if fn == nil {
			return
		}
		if _, ok := calls[fn]; ok {
			return
		}
		calls[fn] = nil
		order = append(order, fn)
		var found []resolved
		for _, call := range a.collectDirectFunctionCalls(fn) {
			callee, ok := a.resolveDirectCallFuncDecl(call)
			if ok && callee != nil {
				found = append(found, resolved{call, callee})
			}
		}
		calls[fn] = found
		for _, c := range found {
			walk(c.callee)
		}
	}
	walk(root)
	parents := map[*ast.FuncDecl][]*ast.FuncDecl{}
	for _, fn := range order {
		for _, c := range calls[fn] {
			parents[c.callee] = append(parents[c.callee], fn)
		}
	}
	reaches := map[*ast.FuncDecl]bool{root: true}
	stack := []*ast.FuncDecl{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range parents[n] {
			if !reaches[p] {
				reaches[p] = true
				stack = append(stack, p)
			}
		}
	}
	var edges []recursionEdge
	for _, fn := range order {
		if reaches[fn] {
			for _, c := range calls[fn] {
				if reaches[c.callee] {
					edges = append(edges, recursionEdge{Caller: fn, Callee: c.callee, Call: c.call})
				}
			}
		}
	}
	return edges
}
func TestRecursiveSCCSinglePassMatchesOriginal(t *testing.T) {
	call := func(name string) ast.Stmt { return &ast.ExprStmt{Expr: &ast.CallExpr{Func: &ast.Ident{Name: name}}} }
	a, b, c := &ast.FuncDecl{Name: "a"}, &ast.FuncDecl{Name: "b"}, &ast.FuncDecl{Name: "c"}
	a.Body = []ast.Stmt{call("b"), call("c"), call("missing"), call("value")}
	b.Body = []ast.Stmt{call("a")}
	c.Body = []ast.Stmt{call("c")}
	global := NewScope(nil)
	for _, fn := range []*ast.FuncDecl{a, b, c} {
		global.Define(&Symbol{Name: fn.Name, Kind: SymbolFunc, Node: fn})
	}
	global.Define(&Symbol{Name: "value", Kind: SymbolLocal, Node: a})
	makeAnalyzer := func(trace bool) *Analyzer {
		an := &Analyzer{file: &ast.File{Filename: "test.elisa"}, globalScope: global, currentScope: NewScope(global), currentFuncDecl: a}
		an.proofLifecycle.Current = &proofContribution{}
		if trace {
			an.lookupTrace = &lookupDependencyRecorder{}
		}
		return an
	}
	for _, trace := range []bool{false, true} {
		left, right := makeAnalyzer(trace), makeAnalyzer(trace)
		want, got := oldRecursiveEdges(left, a), right.collectRecursiveSCCEdges(a)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("trace=%v old=%+v new=%+v", trace, want, got)
		}
		if !reflect.DeepEqual(left.proofLifecycle.Current.Origins, right.proofLifecycle.Current.Origins) {
			t.Fatal("proof dependencies changed")
		}
		if trace && !reflect.DeepEqual(left.lookupTrace.snapshot(), right.lookupTrace.snapshot()) {
			t.Fatal("traced dependencies changed")
		}
	}
}
