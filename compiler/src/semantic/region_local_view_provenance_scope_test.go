package semantic

import (
	"strings"
	"testing"
)

// View provenance is per declaration: `bag` declared in both branches is two locals. The then
// branch's bag holds only a param's view (line 20, accepted); the else branch's bag holds a view
// of a local dstr and still escapes (line 26, rejected). A name-based walk merged them and gave
// up on both.
func TestLocalViewProvenancePerDeclaration(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "local_view_provenance_scope.elisa", `struct Bag:
    names: mutable darray[sview] = []

struct Table:
    names: mutable darray[sview] = []

def keep_first(table: mutable Table&, bag: Bag&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(bag.names[0])

def fill(bag: mutable Bag&, name: sview) -> void:
    can Memory.Allocate:
        bag.names.push(name)

def run(table: mutable Table&, src: sview, flag: bool) -> void:
    can Memory.Allocate, Abort.Panic:
        if flag:
            bag: mutable Bag = Bag{}
            fill(bag, src)
            keep_first(table, &bag)
        else:
            text: mutable dstr = []
            text.push(65)
            bag: mutable Bag = Bag{}
            fill(bag, text.as_sview())
            keep_first(table, &bag)
`)
	errs := result.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0], ":26:") {
		t.Fatalf("expected only line 26 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
}
