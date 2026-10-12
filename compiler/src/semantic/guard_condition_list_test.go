//go:build cgo

package semantic

import (
	"strings"
	"testing"
)

func TestGuardConditionListRequiresBoolOnEveryLine(t *testing.T) {
	for _, value := range []string{"count\n        true", "true\n        count", "count"} {
		src := "def probe(count: i64) -> i64:\n    return 1 if:\n        " + value + "\n    return 2\n"
		result := analyzeTreeTestSourceWithSemanticErrors(t, "guard.elisa", src)
		if len(result.Errors()) == 0 {
			t.Fatalf("non-bool guard accepted: %s", value)
		}
		joined := strings.Join(result.Errors(), "\n")
		if !strings.Contains(joined, "bool") {
			t.Fatalf("missing bool type diagnostic: %s", joined)
		}
	}
	analyzeTreeTestSource(t, "guard.elisa", "def probe(a: bool, b: bool) -> i64:\n    return 1 if:\n        a\n        b\n    return 2\n")
}
