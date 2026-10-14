package semantic

import (
	"strings"
	"testing"
)

// A mutable byte-free local passed where only views are stored out of it: what those views point
// at is everything written into the local. Filled directly (line 27) or by a callee (30) from a
// param -- accepted. Rejected: a view of a local dstr pushed directly (35) or through the callee
// (38); the local's storage aliased into another binding (41, provenance unknown); a filling
// callee that may allocate bytes into the local's own region (44; that callee is itself rejected
// at 21).
func TestLocalViewProvenanceOfMutableStructLocal(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "local_view_provenance.elisa", `struct Bag:
    names: mutable darray[sview] = []
    n: mutable i64 = 0

struct Table:
    names: mutable darray[sview] = []

def keep_first(table: mutable Table&, bag: Bag&) -> void:
    can Memory.Allocate, Abort.Panic:
        table.names.push(bag.names[0])

def fill(bag: mutable Bag&, name: sview) -> void:
    can Memory.Allocate:
        bag.names.push(name)
        bag.n <- bag.n + 1

def fill_own(bag: mutable Bag&) -> void:
    can Memory.Allocate:
        tmp: mutable dstr = []
        tmp.push(66)
        bag.names.push(tmp.as_sview())

def run(table: mutable Table&, other: mutable Table&, src: sview) -> void:
    can Memory.Allocate, Abort.Panic:
        direct: mutable Bag = Bag{}
        direct.names.push(src)
        keep_first(table, &direct)
        filled: mutable Bag = Bag{}
        fill(filled, src)
        keep_first(table, &filled)
        text: mutable dstr = []
        text.push(65)
        bad_direct: mutable Bag = Bag{}
        bad_direct.names.push(text.as_sview())
        keep_first(table, &bad_direct)
        bad_fill: mutable Bag = Bag{}
        fill(bad_fill, text.as_sview())
        keep_first(table, &bad_fill)
        aliased: mutable Bag = Bag{}
        alias: mutable darray[sview] = aliased.names
        keep_first(table, &aliased)
        owned: mutable Bag = Bag{}
        fill_own(owned)
        keep_first(table, &owned)
        other.names.push("x")
`)
	errs := result.Errors()
	want := []string{":21:", ":35:", ":38:", ":41:", ":44:"}
	if len(errs) != len(want) {
		t.Fatalf("expected lines 21, 35, 38, 41, 44 rejected, got:\n%s", strings.Join(errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Fatalf("expected lines 21, 35, 38, 41, 44 rejected, got:\n%s", strings.Join(errs, "\n"))
		}
	}
}
