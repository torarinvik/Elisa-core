package semantic

import (
	"elisacore/src/ast"
	"reflect"
	"testing"
)

// A nested counting loop whose bound is an outer-loop BODY local must NOT lift that bound
// into a pre-loop reserve — the local does not exist at the emission point. Regression:
// `out.reserve(xs.count * n)` was synthesized before the outer loop and errored
// "undefined identifier n" (an unknown-identifier LLVM-lowering crash under -permissive).
func TestAutoReserveNestedBoundBodyLocalSkipped(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(xs: darray[i64]&) -> usize:
    out: mutable darray[i64] = []
    for i in 0..<xs.count:
        n: usize = i + 1
        for j in 0..<n:
            out.push(xs[i])
    return out.count
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("no ForStmt found")
	}
	if loop.PreReserve != nil || len(loop.PreReserves) != 0 {
		t.Fatal("reserve bound referencing a loop-body local must not be hoisted")
	}
}

// Same shape via an iter-for outer loop whose inner bound references the loop VARIABLE
// (a struct with a darray field — the original stage1 machine-port reproducer).
func TestAutoReserveIterNestedBoundLoopVarSkipped(t *testing.T) {
	file := analyzeAndGetFile(t, `struct Item:
    name: sview
    parts: darray[sview]

def collect(items: darray[Item], key: sview) -> darray[sview]:
    out: mutable darray[sview] = []
    for index in 0..<items.count:
        if items[index].name == key:
            found: Item = items[index]
            for part_index in 0..<found.parts.count:
                out.push(found.parts[part_index])
    return out
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("no ForStmt found")
	}
	if loop.PreReserve != nil || len(loop.PreReserves) != 0 {
		t.Fatal("reserve bound referencing a nested struct-copy local must not be hoisted")
	}
}

// Positive control: a nested bound that DOES resolve before the outer loop (a parameter)
// still hoists — the resolvability gate must not disable legitimate nested presizing.
func TestAutoReserveNestedBoundOuterScopeStillHoists(t *testing.T) {
	file := analyzeAndGetFile(t, `def g(n: usize, m: usize) -> usize:
    out: mutable darray[i64] = []
    for i in 0..<n:
        for j in 0..<m:
            out.push(1)
    return out.count
`)
	loop := firstForStmt(file)
	if loop == nil {
		t.Fatal("no ForStmt found")
	}
	if loop.PreReserve == nil {
		t.Fatal("expected nested reserve n*m to hoist when both bounds resolve in the outer scope")
	}
}

// reserveTargetNames lists the receivers of a loop's synthesized reserves, in emission order.
func reserveTargetNames(t *testing.T, preReserves []ast.Stmt) []string {
	t.Helper()
	names := []string{}
	for _, preReserve := range preReserves {
		exprStmt, ok := preReserve.(*ast.ExprStmt)
		if !ok {
			t.Fatalf("expected reserve prelude expr stmt, got %T", preReserve)
		}
		call, ok := exprStmt.Expr.(*ast.CallExpr)
		if !ok {
			t.Fatalf("expected reserve call, got %T", exprStmt.Expr)
		}
		field, ok := call.Func.(*ast.FieldExpr)
		if !ok || field.Field != "reserve" {
			t.Fatalf("expected reserve field call, got %T %#v", call.Func, call.Func)
		}
		recv, ok := field.Object.(*ast.Ident)
		if !ok {
			t.Fatalf("expected identifier reserve target, got %T %#v", field.Object, field.Object)
		}
		names = append(names, recv.Name)
	}
	return names
}

// A loop growing several darrays gets its reserves in the order the body first grows each one,
// on every compile. They used to follow Go's randomized map order, so one source built different
// IR from run to run. Each shape is analyzed repeatedly because a single run can match by chance.
func TestAutoReserveEmitsReservesInFirstGrownOrder(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
		iter bool
	}{
		{
			name: "counting_loop",
			src: `def g(n: usize) -> usize:
    e: mutable darray[i64] = []
    b: mutable darray[i64] = []
    d: mutable darray[i64] = []
    a: mutable darray[i64] = []
    c: mutable darray[i64] = []
    for i in 0..<n:
        d.push(1)
        a.push(2)
        e.push(3)
        c.push(4)
        b.push(5)
        a.push(6)
    return a.count + b.count + c.count + d.count + e.count
`,
			want: []string{"d", "a", "e", "c", "b"},
		},
		{
			name: "iter_loop",
			src: `def g(src: darray[i64]&) -> i64:
    e: mutable darray[i64] = []
    b: mutable darray[i64] = []
    d: mutable darray[i64] = []
    a: mutable darray[i64] = []
    c: mutable darray[i64] = []
    for x in src:
        c.push(x)
        e.push(x)
        b.push(x)
        d.push(x)
        a.push(x)
    return a[0] + b[0] + c[0] + d[0] + e[0]
`,
			want: []string{"c", "e", "b", "d", "a"},
			iter: true,
		},
		{
			// A receiver first grown inside a nested counting loop takes its place in the order
			// at that point; one already grown keeps its earlier place.
			name: "nested_counting_loop",
			src: `def g(n: usize, m: usize) -> usize:
    a: mutable darray[i64] = []
    b: mutable darray[i64] = []
    c: mutable darray[i64] = []
    d: mutable darray[i64] = []
    for i in 0..<n:
        c.push(1)
        for j in 0..<m:
            a.push(2)
            c.push(3)
            d.push(4)
        b.push(5)
    return a.count + b.count + c.count + d.count
`,
			want: []string{"c", "a", "d", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for run := 0; run < 40; run++ {
				file := analyzeAndGetFile(t, tc.src)
				var preReserves []ast.Stmt
				if tc.iter {
					loop := firstIterForStmt(file)
					if loop == nil {
						t.Fatal("no IterForStmt found")
					}
					preReserves = loop.PreReserves
				} else {
					loop := firstForStmt(file)
					if loop == nil {
						t.Fatal("no ForStmt found")
					}
					preReserves = loop.PreReserves
				}
				got := reserveTargetNames(t, preReserves)
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("run %d: reserves emitted for %v, want %v (first-grown order)", run, got, tc.want)
				}
			}
		})
	}
}

// A loop ruled on while diagnostics are suppressed stays undecided, so the reporting pass
// over the same body still emits the lint instead of inheriting a silent ruling.
func TestIterFillLintSuppressedPassDoesNotDecide(t *testing.T) {
	loop := &ast.IterForStmt{}
	a := &Analyzer{suppressDiagnostics: true, pendingIterFillLints: map[*ast.IterForStmt]string{loop: "ys"}}
	a.flushPendingIterFillLint(nil, loop)
	if a.iterFillLintDecided[loop] {
		t.Fatal("a suppressed pass decided the loop")
	}
	a.suppressDiagnostics = false
	a.pendingIterFillLints[loop] = "ys"
	a.flushPendingIterFillLint(nil, loop)
	if a.iterFillLintDecided[loop] || len(a.diagnostics) != 1 {
		t.Fatalf("reporting pass: decided=%v diagnostics=%d", a.iterFillLintDecided[loop], len(a.diagnostics))
	}
	// A retained re-analysis drops the function's diagnostics and analyzes it again: a lint
	// that fired must fire again rather than inherit a remembered ruling.
	a.diagnostics, a.reportedDiagnostics = nil, nil
	a.pendingIterFillLints[loop] = "ys"
	a.flushPendingIterFillLint(nil, loop)
	if len(a.diagnostics) != 1 {
		t.Fatalf("re-analysis: diagnostics=%d, want the lint again", len(a.diagnostics))
	}
}
