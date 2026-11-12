package ownerinventory

import (
	"elisacore/src/ast"
	"testing"
)

func TestScanDeclarationsIncludesNestedStaticBranches(t *testing.T) {
	left, right := &ast.FuncDecl{Name: "left"}, &ast.FuncDecl{Name: "right"}
	nodes := []ast.Decl{&ast.NamespaceDecl{Decls: []ast.Decl{&ast.StaticIfDecl{Then: []ast.Decl{left}, Else: []ast.Decl{right}}}}}
	var got []*ast.FuncDecl
	if !ScanDeclarations(nodes, 0, func() bool { return true }, func(fn *ast.FuncDecl) bool { got = append(got, fn); return true }) || len(got) != 2 || got[0] != left || got[1] != right {
		t.Fatalf("incorrect traversal: %#v", got)
	}
}

func TestScanDeclarationsStopsOnBudgetExhaustion(t *testing.T) {
	visited := 0
	nodes := []ast.Decl{&ast.NamespaceDecl{Decls: []ast.Decl{&ast.FuncDecl{Name: "f"}}}}
	if ScanDeclarations(nodes, 0, func() bool { visited++; return false }, func(*ast.FuncDecl) bool { t.Fatal("must stop before reaching function"); return true }) || visited != 1 {
		t.Fatal("budget did not stop traversal")
	}
}

func TestWalkCandidatesKeepsOrderAndAccountsForWork(t *testing.T) {
	fn := &ast.FuncDecl{Name: "f"}
	decls := []ast.Decl{&ast.NamespaceDecl{Name: "one", Decls: []ast.Decl{fn}}, &ast.NamespaceDecl{Name: "two", Decls: []ast.Decl{fn}}}
	work, skip := 0, 1
	prep := func(origin, qualified int) (int, bool) { return origin + qualified, true }
	reserve := func(used *int, amount, limit int) bool {
		if *used+amount > limit {
			return false
		}
		*used += amount
		return true
	}
	var names []string
	if !WalkCandidates(decls, "", 0, 2, &work, &skip, 100, 100, prep, reserve, func(_ *ast.FuncDecl, name string) bool { names = append(names, name); return true }) {
		t.Fatal("unexpected work exhaustion")
	}
	if len(names) != 1 || names[0] != "two.f" || work != 13 {
		t.Fatalf("names=%v work=%d", names, work)
	}
	work, skip = 0, 0
	if WalkCandidates(decls, "", 0, 2, &work, &skip, 100, 3, prep, reserve, func(*ast.FuncDecl, string) bool { return true }) {
		t.Fatal("expected work budget to stop traversal")
	}
}
