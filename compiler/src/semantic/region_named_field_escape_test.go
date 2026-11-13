package semantic

import (
	"strings"
	"testing"
)

func TestContainerFieldNamedCountKeepsRegion(t *testing.T) {
	result := analyzeTreeTestSourceWithSemanticErrors(t, "count_field_escape.elisa", `struct Box:
    count: darray[i64]
def main() -> i64:
    out: mutable darray[i64] = []
    region scratch(4096):
        xs: mutable darray[i64] = []
        xs.push(42)
        box: Box = Box{count: xs}
        out <- box.count
    return out[0]
`)
	if errs := strings.Join(result.Errors(), "\n"); !strings.Contains(errs, "stored into longer-lived region") {
		t.Fatalf("expected region escape, got %s", errs)
	}
}
