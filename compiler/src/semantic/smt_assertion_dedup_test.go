package semantic

import (
	"strings"
	"testing"
)

func TestSMTDeduplicateAssertions(t *testing.T) {
	padding := "; " + strings.Repeat("x", 4096) + "\n"
	// A comment-only line is deliberately outside the accepted simple form.
	q := padding + "(assert true)\n(assert true)\n"
	if got := smtDeduplicateAssertions(q, ""); got != q {
		t.Fatal("commented query should be left unchanged")
	}
	pad := "(declare-const |" + strings.Repeat("x", 4096) + "| Int)\n"
	goal := "(assert (not (= x 1)))"
	q = pad + "(declare-const x Int)\n(assert (= x 1))\n(assert (= x 1))\n" + goal + "\n" + goal + "\n"
	got := smtDeduplicateAssertions(q, goal)
	if strings.Count(got, "(assert (= x 1))") != 1 || strings.Count(got, goal) != 2 {
		t.Fatalf("duplicate hypothesis or goal mishandled: %s", got[len(pad):])
	}
	for _, suffix := range []string{
		"(push 1)\n", "(reset)\n", "(assert\n true)\n", "garbage\n", "(assert true\n",
	} {
		query := q + suffix
		if got := smtDeduplicateAssertions(query, goal); got != query {
			t.Fatalf("unsafe query changed: %q", suffix)
		}
	}
	query := pad + "(assert true)\n\n(assert true)"
	if got, want := smtDeduplicateAssertions(query, ""), pad+"(assert true)\n\n"; got != want {
		t.Fatal("duplicate without a final newline changed the surrounding text")
	}
	if query := q + "(assert true) (assert false)\n"; smtDeduplicateAssertions(query, goal) != query {
		t.Fatal("multiple commands on a line must leave the input unchanged")
	}
	named := "(assert (! (= x 1) :named n))\n"
	query = pad + named + named
	if got := smtDeduplicateAssertions(query, goal); got != query {
		t.Fatal("named assertions must remain intact")
	}
}

func BenchmarkSMTDeduplicateAssertions(b *testing.B) {
	fact := "(assert (> x " + strings.Repeat("1", 100) + "))\n"
	q := "(declare-const x Int)\n" + strings.Repeat(fact, 400) + "(assert (not (= x 0)))\n"
	b.SetBytes(int64(len(q)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = smtDeduplicateAssertions(q, "(assert (not (= x 0)))")
	}
}
