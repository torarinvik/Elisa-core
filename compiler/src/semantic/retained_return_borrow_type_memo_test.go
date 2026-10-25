package semantic

import (
	"strings"
	"testing"
)

const retainedBorrowPairScalarFixture = `struct Holder:
    view: sview

struct Referent:
    value: u16

def probe(holder: Holder, referent: Referent) -> bool:
    return true
`

// These are two independent memo consumers: one strips a writable holder's
// outer borrow, while the other includes a returned value's outer borrow.
func retainedBorrowPairPredicates() []struct {
	name     string
	cached   func(*Analyzer, Type, Type) bool
	uncached func(*Analyzer, Type, Type) bool
} {
	return []struct {
		name     string
		cached   func(*Analyzer, Type, Type) bool
		uncached func(*Analyzer, Type, Type) bool
	}{
		{"holder", (*Analyzer).returnBorrowMayTarget, (*Analyzer).computeReturnBorrowMayTarget},
		{"result", (*Analyzer).returnBorrowResultMayTarget, func(a *Analyzer, holder, referent Type) bool {
			return a.computeReturnBorrowTargetsReach(holder, referent, true)
		}},
	}
}

func TestRetainedReturnBorrowPairMemosScalarSwap(t *testing.T) {
	t.Setenv("ELISA_PARALLEL_BODIES", "0")
	before := retainedBorrowPairScalarFixture
	after := strings.Replace(before, "    value: u16", "    value: u8", 1)
	for _, predicate := range retainedBorrowPairPredicates() {
		t.Run(predicate.name, func(t *testing.T) {
			initial, retained := AnalyzeRetained(retainedParse(t, before), retainedParse(t, before), []byte(before), AnalyzeOptions{})
			if len(initial.Errors()) != 0 {
				t.Fatalf("fixture rejected before edit: %v", initial.Errors())
			}
			a := retained.a
			holder := a.namedTypes["Holder"].(*StructType)
			referent := a.namedTypes["Referent"].(*StructType)
			if predicate.uncached(a, holder, referent) || predicate.cached(a, holder, referent) {
				t.Fatal("u8 view must not target a record containing only u16")
			}
			key := [2]Type{holder, referent}
			memo := a.returnBorrowMayTargetMemo
			if predicate.name == "result" {
				memo = a.returnBorrowResultMayTargetMemo
			}
			if verdict, present := memo[key]; !present || verdict {
				t.Fatal("old false answer was not actually primed")
			}

			updated, err := retained.Update(retainedParse(t, after), retainedParse(t, after), []byte(after))
			// A declined retained edit is a failed integration regression, even if the
			// separate direct-mutation control below passes. Never silently fall back.
			if err != nil {
				t.Fatalf("scalar swap was not accepted by retained Update: %v", err)
			}
			if retained.LastStats.Fallback != "" {
				t.Fatalf("scalar swap fell back: %+v", retained.LastStats)
			}
			if retained.a != a || updated.NamedTypes["Holder"] != holder || updated.NamedTypes["Referent"] != referent {
				t.Fatal("edit replaced the analyzer or nominal identities; stale-key scenario not exercised")
			}
			if len(updated.Errors()) != 0 {
				t.Fatalf("fixture rejected after scalar swap: %v", updated.Errors())
			}
			if !SameType(referent.Fields["value"].Type, &BuiltinType{Name: "u8"}) {
				t.Fatal("live referent field was not updated")
			}
			if !predicate.uncached(a, holder, referent) {
				t.Fatal("uncached live layout does not expose the new u8 component")
			}

			cold := AnalyzeWithOptions(retainedParse(t, after), AnalyzeOptions{})
			if len(cold.Errors()) != 0 {
				t.Fatalf("cold edited fixture rejected: %v", cold.Errors())
			}
			coldHolder, coldReferent := cold.NamedTypes["Holder"], cold.NamedTypes["Referent"]
			if !predicate.uncached(cold.analyzer, coldHolder, coldReferent) || !predicate.cached(cold.analyzer, coldHolder, coldReferent) {
				t.Fatal("fresh cold analysis does not establish the expected true answer")
			}
			if !predicate.cached(a, holder, referent) {
				t.Fatalf("retained %s Type-pair memo reused its old false answer after accepted scalar swap", predicate.name)
			}
		})
	}
}

// Ground-truth control, separate from the retained acceptance regression. This
// proves that both computations depend on mutable by-value component shape.
func TestReturnBorrowPairPredicatesDependOnScalarShape(t *testing.T) {
	for _, predicate := range retainedBorrowPairPredicates() {
		t.Run(predicate.name, func(t *testing.T) {
			a := &Analyzer{}
			holder := &StructType{Name: "Holder", Fields: map[string]Field{"view": {Type: &SViewType{}}}}
			referent := &StructType{Name: "Referent", Fields: map[string]Field{"value": {Type: &BuiltinType{Name: "u16"}}}}
			if predicate.uncached(a, holder, referent) {
				t.Fatal("unexpected target before scalar mutation")
			}
			referent.Fields["value"] = Field{Type: &BuiltinType{Name: "u8"}}
			if !predicate.uncached(a, holder, referent) {
				t.Fatal("scalar mutation did not change the uncached answer")
			}
		})
	}
}
