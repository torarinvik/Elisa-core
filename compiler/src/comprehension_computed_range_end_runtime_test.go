package main

import (
	"strings"
	"testing"
)

// A comprehension over `0..<EXPR` with a computed end takes the presized indexed-store path:
// the end is bound once to a synthetic local, so it is evaluated exactly once and the result
// matches the push fallback element for element.
const comprehensionComputedRangeEndBody = `
struct Counter:
    calls: mutable i64

def bump(c: mutable Counter&, n: i64) -> usize:
    c.calls <- c.calls + 1
    (n + 1).usize()

def squares(n: i64) -> darray[i64]:
    [i.i64() * i.i64() for i in 0..<(n + 1).usize()]

@test
def comprehension_computed_range_end() -> void:
    can Abort.Panic:
        c: mutable Counter = Counter{calls: 0}
        xs: darray[i64] = [7 for _ in 0..<bump(&c, 4)]
        if c.calls != 1 or xs.count != 5.usize() or xs[4] != 7:
            panic("computed end evaluated once")
        ys: darray[i64] = squares(9)
        if ys.count != 10.usize() or ys[3] != 9 or ys[9] != 81:
            panic("computed end contents")
        zs: darray[i64] = squares(0)
        if zs.count != 1.usize() or zs[0] != 0:
            panic("single-element computed range")
`

func TestComprehensionComputedRangeEnd(t *testing.T) {
	t.Parallel()
	exit, stdout, stderr := runStressProgram(t, "comprehension_computed_range_end", comprehensionComputedRangeEndBody)
	assertAllPassed(t, exit, stdout, stderr, "comprehension_computed_range_end")
	_, ir, _ := compileStressProgram(t, "comprehension_computed_range_end_ir", comprehensionComputedRangeEndBody)
	start := strings.Index(ir, "@squares(")
	if start < 0 {
		t.Fatalf("squares not found in IR")
	}
	body := ir[start:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	if strings.Contains(body, "darray.push.realloc") {
		t.Fatalf("computed-end comprehension still lowers to the push loop")
	}
}
