package semantic

import (
	"strings"
	"testing"
)

// Diagnostics are printed in append order, so a check that reports once per struct field must walk
// the (map-typed) field set in declaration order: three private fields of a zeroed value report in
// the order they were declared, on every run.
func TestPrivateZeroedFieldDiagnosticsFollowDeclarationOrder(t *testing.T) {
	src := `
module Vault:
    struct Handle:
        private:
            zeta: i64
            alpha: i64
            mid: i64
        public:
            tag: i64
def main() -> i64:
    h: Vault::Handle = zeroed
    h.tag
`
	var first string
	for run := 0; run < 30; run++ {
		result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "private_order.elisa", src, AnalyzeOptions{})
		var private []string
		for _, d := range result.Diagnostics {
			if strings.Contains(d.Message, "is private to module") {
				private = append(private, d.Message)
			}
		}
		if len(private) != 3 {
			t.Fatalf("expected 3 privacy diagnostics, got %d:\n%s", len(private), strings.Join(private, "\n"))
		}
		joined := strings.Join(private, "\n")
		zeta, alpha, mid := strings.Index(joined, "zeta"), strings.Index(joined, "alpha"), strings.Index(joined, "mid")
		if zeta < 0 || alpha < 0 || mid < 0 || !(zeta < alpha && alpha < mid) {
			t.Fatalf("diagnostics not in declaration order:\n%s", joined)
		}
		if run == 0 {
			first = joined
		} else if joined != first {
			t.Fatalf("run %d differs:\n%s\n---\n%s", run, first, joined)
		}
	}
}

func TestSortedSymbolKeysOrdersByNameThenPosition(t *testing.T) {
	m := map[*Symbol]int{
		{Name: "b"}: 1,
		{Name: "a"}: 2,
		{Name: "c"}: 3,
	}
	var names []string
	for _, sym := range sortedSymbolKeys(m) {
		names = append(names, sym.Name)
	}
	if got := strings.Join(names, ","); got != "a,b,c" {
		t.Fatalf("got %s", got)
	}
}
