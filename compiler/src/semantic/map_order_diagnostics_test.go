package semantic

import (
	"strings"
	"testing"

	"elisacore/src/lexer"
	"elisacore/src/parser"
)

func mapOrderDiagnostics(t *testing.T, src string) string {
	t.Helper()
	p := parser.New(lexer.New("order.elisa", []byte(src)).Tokenize())
	file := p.ParseFile("order.elisa")
	if errs := p.Errors(); len(errs) != 0 {
		t.Fatalf("parse errors: %v", errs)
	}
	return strings.Join(Analyze(file).Errors(), "\n")
}

// Several diagnostics were emitted while ranging over a map (an interface's Methods and
// AssociatedTypes, machine states, condition bindings, store targets, named types, region
// joins), so two missing impl methods were listed in a different order run to run. Found by
// FuzzChecker's double-analysis check. Reports are now in name order.
func TestImplMissingMethodsReportedInNameOrder(t *testing.T) {
	src := "struct Num:\n    a: i32\n\nprotocol Ord:\n    def zeta(self: Num) -> i32\n    def alpha(self: Num) -> i32\n    def mid(self: Num) -> i32\n\nimpl Ord for Num:\n    def mid(self: Num) -> i32:\n        return 0\n"
	first := mapOrderDiagnostics(t, src)
	ia, iz := strings.Index(first, `missing method "alpha"`), strings.Index(first, `missing method "zeta"`)
	if ia < 0 || iz < 0 || ia > iz {
		t.Fatalf("expected alpha before zeta, got:\n%s", first)
	}
	for i := 0; i < 30; i++ {
		if got := mapOrderDiagnostics(t, src); got != first {
			t.Fatalf("run %d differs:\n%s\n=== vs ===\n%s", i, first, got)
		}
	}
}
