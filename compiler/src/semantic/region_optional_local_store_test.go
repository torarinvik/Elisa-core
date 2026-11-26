package semantic

import (
	"strings"
	"testing"
)

// SOUNDNESS: a struct value built in the function's own inferred region and stored into a field
// reached through a reference parameter dangles once the function returns. The plain-struct
// local form was already rejected; an OPTIONAL local (`bag: Bag? = make_bag()`) and a local
// or direct value unwrapped with `get make_bag() else ...` were not recorded as region-backed,
// compiled, and read freed memory at runtime.
func TestOptionalAndGetUnwrappedLocalStoresThroughRefRejected(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "optional_local_store.elisa", `struct Item:
    value: i64

struct Bag:
    items: mutable darray[Item]

struct Prover:
    bag: mutable Bag?
    plain: mutable Bag

def make_bag(seed: i64) -> Bag? can[Memory.Allocate]:
    items: mutable darray[Item] = []
    items.push(Item{value: seed})
    Bag{items: items}

def optional_local(prover: mutable Prover&) -> void can[Memory.Allocate]:
    bag: Bag? = make_bag(7)
    prover.bag <- bag

def unwrapped_local(prover: mutable Prover&) -> bool can[Memory.Allocate]:
    bag: Bag = get make_bag(7) else return false
    prover.bag <- bag
    true

def unwrapped_direct(prover: mutable Prover&) -> bool can[Memory.Allocate]:
    prover.plain <- get make_bag(7) else return false
    true
`)
	joined := strings.Join(result.Errors(), "\n")
	for _, line := range []string{":18:", ":22:", ":26:"} {
		if !strings.Contains(joined, "optional_local_store.elisa"+line) {
			t.Errorf("want the store on line %s rejected, got:\n%s", strings.Trim(line, ":"), joined)
		}
	}
}

// The destination-driven form stays accepted: a region-polymorphic call stored straight into
// the field allocates in the caller's region, so nothing dangles.
func TestOptionalCallStoredStraightThroughRefAccepted(t *testing.T) {
	result := analyzeFunctionAnalysisTestSourceWithSemanticErrors(t, "optional_direct_store.elisa", `struct Item:
    value: i64

struct Bag:
    items: mutable darray[Item]

struct Prover:
    bag: mutable Bag?

def make_bag(seed: i64) -> Bag? can[Memory.Allocate]:
    items: mutable darray[Item] = []
    items.push(Item{value: seed})
    Bag{items: items}

def adopt(prover: mutable Prover&) -> void can[Memory.Allocate]:
    prover.bag <- make_bag(7)
`)
	if errors := result.Errors(); len(errors) != 0 {
		t.Fatalf("want the direct optional store accepted, got:\n%s", strings.Join(errors, "\n"))
	}
}
