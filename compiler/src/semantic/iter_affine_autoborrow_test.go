package semantic

import (
	"strings"
	"testing"

	"elisacore/src/ast"
)

// Read-only iteration over affine elements AUTO-BORROWS: `for x in xs` upgrades the
// binding to by-reference when a borrow is legal (a user-declared linear struct is a
// borrowable owner), instead of erroring "use ref" like it did before the `ref`
// binder spelling was removed. Copy-vs-ref on an immutable binding is semantically
// invisible, so the mechanism is the compiler's decision.
func TestAnalyzeAffineReadOnlyIterationAutoBorrows(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "iter_affine_autoborrow.elisa", `linear struct Guard:
    active: bool
def make() -> Guard:
    return Guard{active: true}
def release(g: Guard) -> void:
    _ = move g

def scan() -> i64:
    can Memory.Allocate, Memory.Release, Abort.Panic:
        gs: mutable darray[Guard] = []
        gs.push(make())
        gs.push(make())
        live: mutable i64 = 0
        for g in gs:
            if g.active:
                live <- live + 1
        for g in move gs:
            release(move g)
        return live
`)
	if errs := strings.Join(result.Errors(), " | "); errs != "" {
		t.Fatalf("read-only iteration over borrowable affine elements must compile (auto-borrow), got: %s", errs)
	}
	var loop *ast.IterForStmt
	var findLoop func(stmts []ast.Stmt)
	findLoop = func(stmts []ast.Stmt) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.IterForStmt:
				if loop == nil && !s.MovedSource {
					loop = s
				}
			case *ast.CanStmt:
				findLoop(s.Body)
			case *ast.RegionStmt:
				findLoop(s.Body)
			case *ast.InStoreStmt:
				findLoop(s.Body)
			}
		}
	}
	for _, decl := range result.File.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name == "scan" {
			findLoop(fn.Body)
		}
	}
	if loop == nil {
		t.Fatal("expected the read-only iter loop to be found")
	}
	if loop.Mode != ast.IterBindRef {
		t.Fatalf("expected the analyzer to auto-upgrade the read-only affine loop to a ref binding, got %v", loop.Mode)
	}
}

// Elements that contain linear handles but are NOT borrowable owners still error —
// now with the move-drain guidance (the removed `ref` advice would be a dead end).
func TestAnalyzeAffineNonBorrowableIterationStillErrors(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "iter_affine_nonborrowable.elisa", `struct Slot:
    t: Thread

def drain(xs: darray[Slot]) -> void:
    for x in xs:
        pass
`)
	all := strings.Join(result.Errors(), "\n")
	if !strings.Contains(all, "cannot copy or borrow the elements") {
		t.Fatalf("expected the non-borrowable affine iteration diagnostic, got:\n%s", all)
	}
}
