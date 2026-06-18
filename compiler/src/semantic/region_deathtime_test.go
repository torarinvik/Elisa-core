package semantic

import (
	"testing"

	"elisacore/src/ast"
)

// withDeathTimeDump enables the read-only G0 cohort recording for the duration of fn.
func withDeathTimeDump(t *testing.T, fn func()) {
	t.Helper()
	prev := dumpDeathTime
	dumpDeathTime = true
	defer func() { dumpDeathTime = prev }()
	fn()
}

func cohortContaining(cohorts []DeathTimeCohort, name string) (DeathTimeCohort, bool) {
	for _, c := range cohorts {
		for _, n := range c.Allocs {
			if n == name {
				return c, true
			}
		}
	}
	return DeathTimeCohort{}, false
}

// docs/91 G0: inferred allocations are grouped into death cohorts. Distinct last-use points →
// distinct cohorts; an escaping (returned) allocation → the escapes cohort (DeathIndex -1).
func TestDeathTimeCohortsDistinctAndEscape(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_distinct.elisa", `def f() -> darray[i64]:
    can Memory.Allocate, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        c: mutable darray[i64] = []
        a.push(1)
        b.push(2)
        c.push(3)
        c.push(4)
        return a
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["f"]
	if len(cohorts) != 3 {
		t.Fatalf("expected 3 cohorts (a escapes, b and c die at different points), got %d: %+v", len(cohorts), cohorts)
	}
	if c, ok := cohortContaining(cohorts, "a"); !ok || c.DeathIndex != -1 {
		t.Fatalf("returned `a` must be in the escapes cohort (DeathIndex -1), got ok=%v cohort=%+v", ok, c)
	}
	cb, okb := cohortContaining(cohorts, "b")
	cc, okc := cohortContaining(cohorts, "c")
	if !okb || !okc {
		t.Fatalf("b and c must be present, got b=%v c=%v", okb, okc)
	}
	if cb.DeathIndex == cc.DeathIndex {
		t.Fatalf("b (last used earlier) and c (last used later) must be in different cohorts, both died @%d", cb.DeathIndex)
	}
}

// Allocations last used at the SAME statement share a cohort (they die together).
func TestDeathTimeCohortsSharedDeathGroups(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_shared.elisa", `def g() -> void:
    can Memory.Allocate, Abort.Panic:
        x: mutable darray[i64] = []
        y: mutable darray[i64] = []
        x.push(1)
        y.push(2)
        x.push(y.count.i64())
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["g"]
	cx, okx := cohortContaining(cohorts, "x")
	cy, oky := cohortContaining(cohorts, "y")
	if !okx || !oky {
		t.Fatalf("x and y must be present, got x=%v y=%v cohorts=%+v", okx, oky, cohorts)
	}
	if cx.DeathIndex != cy.DeathIndex {
		t.Fatalf("x and y are both last used at the final statement and must share a cohort, got x@%d y@%d", cx.DeathIndex, cy.DeathIndex)
	}
	if cx.Growables != 2 {
		t.Fatalf("the shared cohort should report 2 growables (each gets its own stack), got %d", cx.Growables)
	}
}

// Loop-aware liveness (docs/91 G0 hardening): two allocations declared before a loop and used at
// DIFFERENT statements inside it both die when the loop EXITS (their uses can recur on the
// back-edge), so they share one cohort. With plain lexical last-mention they would have landed in
// two different cohorts (the two distinct in-loop push statements).
func TestDeathTimeLoopLiftsUsesToLoopExit(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_loop.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        a: mutable darray[i64] = []
        b: mutable darray[i64] = []
        i: mutable i64 = 0
        while i < 3:
            a.push(i)
            b.push(i)
            i <- i + 1
        return 0
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["f"]
	ca, oka := cohortContaining(cohorts, "a")
	cb, okb := cohortContaining(cohorts, "b")
	if !oka || !okb {
		t.Fatalf("a and b must be present, got a=%v b=%v cohorts=%+v", oka, okb, cohorts)
	}
	if ca.DeathIndex != cb.DeathIndex {
		t.Fatalf("a and b are both live across the loop, so loop-lifting must put them in one cohort (same loop-exit death), got a@%d b@%d", ca.DeathIndex, cb.DeathIndex)
	}
}

// --- pure-helper unit tests (no analyzer needed) ---

func TestStmtMentionsName(t *testing.T) {
	// `foo.push(bar)` mentions both foo and bar, not baz.
	stmt := &ast.ExprStmt{Expr: &ast.CallExpr{
		Func: &ast.FieldExpr{Object: &ast.Ident{Name: "foo"}, Field: "push"},
		Args: []ast.Expr{&ast.Ident{Name: "bar"}},
	}}
	if !stmtMentionsName(stmt, "foo") {
		t.Fatal("expected foo to be mentioned")
	}
	if !stmtMentionsName(stmt, "bar") {
		t.Fatal("expected bar to be mentioned")
	}
	if stmtMentionsName(stmt, "baz") {
		t.Fatal("baz is not mentioned")
	}
}

func TestChildStmtBlocksCoversControlFlow(t *testing.T) {
	ifs := &ast.IfStmt{
		Then:  []ast.Stmt{&ast.ReturnStmt{}},
		Elifs: []ast.ElifClause{{Body: []ast.Stmt{&ast.ReturnStmt{}}}},
		Else:  []ast.Stmt{&ast.ReturnStmt{}},
	}
	blocks := childStmtBlocks(ifs)
	if len(blocks) != 3 {
		t.Fatalf("if/elif/else should yield 3 child blocks, got %d", len(blocks))
	}
	if len(childStmtBlocks(&ast.WhileStmt{Body: []ast.Stmt{&ast.ReturnStmt{}}})) != 1 {
		t.Fatal("while should yield 1 child block")
	}
}
