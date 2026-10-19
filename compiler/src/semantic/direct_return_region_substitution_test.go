package semantic

import (
	"elisacore/src/ast"
	"fmt"
	"strings"
	"testing"
)

func TestDirectRegionReturnSubstitution(t *testing.T) {
	cases := []struct{ name, typ string }{
		{"darray", "darray[u8]"},
		{"ref", "i64&"},
		{"view", "view[u8]"},
		{"sview", "sview"},
	}
	for _, tc := range cases {
		for _, names := range [][2]string{{"b", "c"}, {"b", "a"}, {"a", "b"}} {
			t.Run(tc.name+"/"+names[0]+names[1], func(t *testing.T) {
				sourceType := func(region string) string { return tc.typ + " @" + region }
				src := fmt.Sprintf(`def choose[@a, @b](left: %s, right: %s) -> %s:
    return left

def caller[@%s, @%s](left: %s, right: %s) -> void:
    result: %s = choose(left, right)

def main() -> i64:
    return 0
`, sourceType("a"), sourceType("b"), sourceType("a"), names[0], names[1], sourceType(names[0]), sourceType(names[1]), sourceType(names[0]))
				r := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, tc.name+"_region_return.elisa", src, AnalyzeOptions{})
				diagnostics := strings.Join(r.Errors(), "\n")
				t.Logf("diagnostics=%s", diagnostics)
				found := false
				for expr, typ := range r.analyzer.exprTypes {
					if call, ok := expr.(*ast.CallExpr); ok && callIdentName(call) == "choose" {
						found = true
						got := regionParamReturnTypeRegion(typ)
						t.Logf("result=%s region=%q", typeString(typ), got)
						if tc.name == "sview" {
							owner := r.analyzer.declaredCallReturnRegions[call]
							t.Logf("return owner=%q", owner)
							if owner != names[0] {
								t.Fatalf("direct sview return owner=%q, want %q", owner, names[0])
							}
						} else if got != names[0] {
							t.Fatalf("direct %s result region=%q, want %q", tc.name, got, names[0])
						}
					}
				}
				if !found {
					t.Fatal("choose call missing")
				}
				if diagnostics != "" {
					t.Fatalf("unexpected diagnostics after simultaneous substitution: %s", diagnostics)
				}
			})
		}
	}
}
