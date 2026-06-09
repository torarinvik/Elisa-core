package semantic

import (
	"strings"
	"testing"
)

// `dview[T]` has been removed; the one borrowed-view spelling is `view[T]`.
func TestDviewSpellingRemoved(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "dview_removed.elisa", `def keep(items: dview[i64]) -> usize:
    return items.len
`)
	if !strings.Contains(strings.Join(result.Errors(), "\n"), "`dview[T]` has been removed; use `view[T]`") {
		t.Fatalf("expected dview removal diagnostic, got: %v", result.Errors())
	}
}
