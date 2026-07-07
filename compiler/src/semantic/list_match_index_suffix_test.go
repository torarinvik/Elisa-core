package semantic

import (
	"strings"
	"testing"
)

// List match patterns (`[a, b]:` / `[a, b, ...rest]:`) synthesize an IndexExpr per
// bound element to type its binding. Those synthesized index literals must NOT carry a
// `u` suffix: the index position already infers usize, and a suffix tripped the
// discouraged-numeric-literal-suffix lint at the pattern's own source location — a
// spurious warning on ordinary user code that never wrote a suffix.
func TestListMatchIndexLiteralsAreSuffixless(t *testing.T) {
	result := analyzeFunctionAnalysisTestSource(t, "list_match_index_suffix.elisa", `
def f(xs: darray[i64]) -> i64:
    match xs:
        [a, b]:
            return a + b
        [a, b, c]:
            return a + b + c
        _:
            return 0
`)
	for _, w := range result.Warnings() {
		if strings.Contains(w, "numeric literal suffix") {
			t.Fatalf("list match synthesized a suffixed index literal, tripping the deprecation lint: %q", w)
		}
	}
}
