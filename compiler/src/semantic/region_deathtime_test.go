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

// Alias-aware death (docs/91 G0 hardening): a value used only through an alias must stay live until
// the ALIAS's last use, not just its own last direct mention. Here `v` is aliased by the view `w`,
// and `w` is used at the same statement that uses `x`; alias-awareness must extend `v`'s life to
// there, putting `v` and `x` in the same cohort. Without it, `v` would die at the earlier `w = v[..]`
// statement — a different (too-early) cohort, which would be a use-after-free once G1 frees on this.
func TestDeathTimeAliasExtendsLife(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_alias.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        v: mutable darray[i64] = []
        x: mutable darray[i64] = []
        v.push(1)
        w: view[i64] = v[0:1]
        x.push(w[0].i64())
        return 0
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["f"]
	cv, okv := cohortContaining(cohorts, "v")
	cx, okx := cohortContaining(cohorts, "x")
	if !okv || !okx {
		t.Fatalf("v and x must be present, got v=%v x=%v cohorts=%+v", okv, okx, cohorts)
	}
	if cv.DeathIndex != cx.DeathIndex {
		t.Fatalf("alias `w` of `v` is used at the same statement as `x`, so alias-awareness must keep v live there (same cohort), got v@%d x@%d", cv.DeathIndex, cx.DeathIndex)
	}
}

// A value passed to a callee that RETAINS the argument (here `retain` returns it — a storage return)
// escapes: its death is deferred to the caller (cohort DeathIndex -1).
func TestDeathTimeRetainingCallArgEscapes(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_retain_arg.elisa", `def retain(d: darray[i64]) -> darray[i64]:
    return d

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        v: mutable darray[i64] = []
        v.push(1)
        keep: darray[i64] = retain(v)
        return keep[0]
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["f"]
	cv, ok := cohortContaining(cohorts, "v")
	if !ok {
		t.Fatalf("v must be present, cohorts=%+v", cohorts)
	}
	if cv.DeathIndex != -1 {
		t.Fatalf("v is passed to a retaining callee (returns the arg) and must escape (DeathIndex -1), got @%d", cv.DeathIndex)
	}
}

// THE INTERPROCEDURAL WIN (docs/91): a value passed to a callee that only READS it (returns a scalar)
// is NOT retained, so it is reclaimed as an in-function death rather than conservatively escaping —
// the uplift the arg-retention summaries deliver.
func TestDeathTimeReaderCallArgReclaimed(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_reader_arg.elisa", `def total(d: darray[i64]) -> i64:
    can Abort.Panic:
        s: mutable i64 = 0
        for x in d:
            s <- s + x
        return s

def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        v: mutable darray[i64] = []
        v.push(1)
        r: i64 = total(v)
        return r
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["f"]
	cv, ok := cohortContaining(cohorts, "v")
	if !ok {
		t.Fatalf("v must be present, cohorts=%+v", cohorts)
	}
	if cv.DeathIndex == -1 {
		t.Fatalf("v is passed only to a READER (total returns a scalar; does not retain), so it must be reclaimed as an in-function death, not escape")
	}
}

// Control: a method RECEIVER (`v.push(..)`) is NOT a call argument, so it must not be flagged as
// escaping — v stays a normal in-function cohort.
func TestDeathTimeMethodReceiverNotEscape(t *testing.T) {
	var result *Result
	withDeathTimeDump(t, func() {
		result = analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "dt_receiver.elisa", `def f() -> i64:
    can Memory.Allocate, Abort.Panic:
        v: mutable darray[i64] = []
        v.push(1)
        v.push(2)
        return 0
`, AnalyzeOptions{})
	})
	cohorts := result.DeathTimeCohorts["f"]
	cv, ok := cohortContaining(cohorts, "v")
	if !ok {
		t.Fatalf("v must be present, cohorts=%+v", cohorts)
	}
	if cv.DeathIndex == -1 {
		t.Fatalf("v is only a method receiver (not a call argument), so it must NOT be flagged escaping")
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
