package parser

import "testing"

// A local named `guard`/`require` whose initializer is a `get … else` is a binding, not
// a `guard COND else …` statement: the guard scan used to see the `else` on the line
// and report "unexpected token : in expression".
func TestGuardNamedBindingWithGetElse(t *testing.T) {
	for _, src := range []string{
		"def g(x: i64) -> i64?:\n    x\n\ndef f(n: i64) -> i64?:\n    guard: i64 = get g(n) else return null\n    guard\n",
		"def g(x: i64) -> i64?:\n    x\n\ndef f(n: i64) -> i64?:\n    require: i64 = get g(n) else return null\n    require\n",
		"def g(x: i64) -> i64?:\n    x\n\ndef f(n: i64) -> i64?:\n    guard: mutable i64 = 0\n    guard <- get g(n) else return null\n    guard\n",
	} {
		if _, errs, _ := parseSourceWithNotices(t, src); len(errs) != 0 {
			t.Fatalf("unexpected parser errors for %q: %v", src, errs)
		}
	}
}

func TestGuardStatementStillParses(t *testing.T) {
	src := "def f(n: i64) -> i64:\n    guard n > 0 else return 0\n    n\n"
	if _, errs, _ := parseSourceWithNotices(t, src); len(errs) != 0 {
		t.Fatalf("unexpected parser errors: %v", errs)
	}
}
