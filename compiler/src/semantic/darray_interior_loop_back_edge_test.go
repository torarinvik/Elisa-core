package semantic

import (
	"strings"
	"testing"
)

const loopBackEdgeStale = "storage dependency facts were invalidated"

// An interior reference taken before a loop and read at the top of the body is stale on the second
// iteration when the body grows the container after the read. The forward walk sees the read only
// in the entry state, so each loop checks its back edge (fall-through and `continue` states)
// against the first use of every view declared outside it.
func TestInteriorRefStaleOnLoopBackEdge(t *testing.T) {
	cases := map[string]string{
		"range_for": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        total <- total + r
        xs.push(i)
    return total
`,
		"while_body": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    i: mutable i64 = 0
    while i < n:
        total <- total + r
        xs.push(i)
        i <- i + 1
    return total
`,
		"while_condition": `def f(xs: mutable darray[i64]&) -> i64:
    r: i64& = &xs[0]
    mutable i: i64 = 0
    while i < r:
        xs.push(i)
        i <- i + 1
    return i
`,
		"callee_growth": `def grow(ys: mutable darray[i64]&) -> void:
    ys.push(9)

def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        total <- total + r
        grow(xs)
    return total
`,
		"continue_path": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        total <- total + r
        if i > 2:
            xs.push(i)
            continue
        total <- total + 1
    return total
`,
		"inner_loop_use": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        for j in 0..<n |total, r|:
            total <- total + r
        xs.push(i)
    return total
`,
		"branch_rebind": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    mutable r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        if i > 1:
            r <- &xs[0]
        total <- total + r
        xs.push(i)
    return total
`,
	}
	for name, source := range cases {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "loop_back_edge_"+name+".elisa", source, AnalyzeOptions{})
		if !strings.Contains(allDiagnostics(result), loopBackEdgeStale) {
			t.Fatalf("%s: expected the interior ref read after a previous iteration's growth to be rejected, got:\n%s", name, allDiagnostics(result))
		}
	}
}

func TestInteriorRefLoopBackEdgeAcceptsFreshViews(t *testing.T) {
	cases := map[string]string{
		"declared_in_body": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    total: mutable i64 = 0
    for i in 0..<n |total, xs|:
        r: i64& = &xs[0]
        total <- total + r
        xs.push(i)
    return total
`,
		"rebound_first": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    mutable r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        r <- &xs[0]
        total <- total + r
        xs.push(i)
    return total
`,
		"always_breaks": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        total <- total + r
        xs.push(i)
        break
    return total
`,
		"rebound_at_end": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    mutable r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        total <- total + r
        xs.push(i)
        r <- &xs[0]
    return total
`,
		"growth_breaks": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    r: i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, r|:
        total <- total + r
        if i > 2:
            xs.push(i)
            break
    return total
`,
	}
	for name, source := range cases {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "loop_back_edge_ok_"+name+".elisa", source, AnalyzeOptions{})
		if diags := allDiagnostics(result); strings.TrimSpace(diags) != "" {
			t.Fatalf("%s: expected no diagnostics, got:\n%s", name, diags)
		}
	}
}

// A store through a mutable interior reference uses the referent exactly as a read does: after a
// push may have moved the storage, `w <- v` writes into freed memory. Before this, only reads were
// checked and the store was accepted.
func TestInteriorRefStaleWriteThrough(t *testing.T) {
	stale := map[string]string{
		"straight": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    w: mutable i64& = &xs[0]
    xs.push(n)
    w <- 5
    return n
`,
		"loop_back_edge": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    w: mutable i64& = &xs[0]
    for i in 0..<n |xs, w|:
        w <- 5
        xs.push(i)
    return n
`,
		"store_is_not_a_rebind": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    w: mutable i64& = &xs[0]
    total: mutable i64 = 0
    for i in 0..<n |total, xs, w|:
        w <- 5
        total <- total + w
        xs.push(i)
    return total
`,
		"store_keeps_tracking": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    w: mutable i64& = &xs[0]
    w <- 5
    xs.push(n)
    return w
`,
	}
	for name, source := range stale {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "write_through_"+name+".elisa", source, AnalyzeOptions{})
		if !strings.Contains(allDiagnostics(result), loopBackEdgeStale) {
			t.Fatalf("%s: expected the store through a stale interior ref to be rejected, got:\n%s", name, allDiagnostics(result))
		}
	}
	fresh := map[string]string{
		"before_growth": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    w: mutable i64& = &xs[0]
    w <- 5
    xs.push(n)
    return n
`,
		"rebound_after_growth": `def f(xs: mutable darray[i64]&, n: i64) -> i64:
    w: mutable i64& = &xs[0]
    xs.push(n)
    w <- &xs[0]
    w <- 5
    return n
`,
	}
	for name, source := range fresh {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "write_through_ok_"+name+".elisa", source, AnalyzeOptions{})
		if diags := allDiagnostics(result); strings.TrimSpace(diags) != "" {
			t.Fatalf("%s: expected no diagnostics, got:\n%s", name, diags)
		}
	}
}
