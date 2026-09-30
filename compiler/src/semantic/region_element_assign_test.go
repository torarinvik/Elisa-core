package semantic

import (
	"strings"
	"testing"
)

// Assigning a tracked container element into a plain local keeps the element's provenance, so the
// local stored into the caller's table is not attributed to the coarse inferred region.
func TestLocalContainerElementAssignKeepsProvenance(t *testing.T) {
	src := `struct Row:
    name: sview

struct Diag:
    expected: sview

struct Table:
    diagnostics: mutable darray[Diag]

def check(rows: darray[Row]&, table: lmut Table) -> void can[Abort.Panic, Memory.Allocate]:
    can Abort.Panic, Memory.Allocate:
        destinations: darray[sview] = [r.name for r in rows]
        first: mutable sview = ""
        first <- destinations[0]
        table.diagnostics <- table.diagnostics.push(Diag{expected: first})

def main() -> i32:
    return 0
`
	result := analyzeFunctionAnalysisTestSource(t, "element_assign_provenance.elisa", src)
	if errs := result.Errors(); len(errs) != 0 {
		t.Fatalf("must be allowed, got: %s", strings.Join(errs, "\n"))
	}
}
