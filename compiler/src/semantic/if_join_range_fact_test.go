package semantic

import (
	"strings"
	"testing"
)

// A range fact a branch invalidated (by writing the variable) must not survive the if-join as the
// entry fact: `x: mutable = 0; if c: x <- 7; return x` with `ensure result == 0` once proved by the
// stale entry range [0, 0]. The same hole hid every capture-list `while` (desugared to `if true:`).
// KNOWN OPEN (not ported): the branch's capture_while / nested_while cases (a `while` whose body
// writes the result) still prove `ensure result == 0` on this tree. The fix is selfhost-proofs
// 56ce8eec (loop bodies drop loop-entry facts of written places), which regresses six main loop
// tests until the branch's loop-path invariant machinery (3b0833af and successors) is ported too.
func TestIfJoinDropsInvalidatedRangeFact(t *testing.T) {
	cases := map[string]string{
		"if_write": `def w(n: i64) -> i64:
    ensure result == 0
    index: mutable i64 = 0
    if n >= 0:
        index <- 7
    return index
`,
		"if_increment": `def w(n: i64) -> i64:
    ensure result == 0
    return 0 if n < 0
    index: mutable i64 = 0
    if n >= 0:
        index <- index + 1
    return index
`,
	}
	for name, src := range cases {
		if errs := ifJoinErrs(t, name+".elisa", src); !strings.Contains(errs, `ensure postcondition of "w"`) {
			t.Errorf("%s: a branch-written result must not prove ensure result == 0, got:\n%s", name, errs)
		}
	}
	// The join still keeps facts every path agrees on.
	ok := `def w(n: i64) -> i64:
    ensure result >= 0 and result <= 7
    index: mutable i64 = 0
    if n >= 0:
        index <- 7
    return index
`
	if errs := ifJoinErrs(t, "if_join_ok.elisa", ok); errs != "" {
		t.Fatalf("the joined range [0, 7] must prove, got:\n%s", errs)
	}
}

// ifJoinErrs is the strict-proof error list (selfhost-misc's loopPathErrs, without the solver-error
// counter that branch adds to SMTProfile).
func ifJoinErrs(t *testing.T, name, src string) string {
	t.Helper()
	return strings.Join(analyzeContractStrict(t, name, src).Errors(), "\n")
}
