package semantic

import (
	"strings"
	"testing"
)

func TestPitOfSuccessKeepsUncheckedIndexStrictAsError(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithOptionsAllowingDiagnostics(t, "pit_unchecked_index_error.elisa", `def f(xs: darray[i64]&, i: usize) -> i64:
    return xs[i]
`, AnalyzeOptions{EnforceUnsafePermissions: true})
	all := allDiagnostics(result)
	if !strings.Contains(all, "unchecked index requires") {
		t.Fatalf("expected unchecked index strict error, got:\n%s", all)
	}
	if len(result.Errors()) == 0 {
		t.Fatal("strict unchecked index friction should be an error")
	}
}
